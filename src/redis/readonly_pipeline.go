package redis

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp"
	"github.com/mediocregopher/radix/v4/resp/resp3"
)

// readOnlyPipeline is a radix.Action that behaves like radix.NewPipeline(),
// except that a READONLY reply to any of its commands marks the connection as
// unusable, so the pool discards it.
//
// readOnlyClosingConn cannot do this for pipelines. radix's pipeline stores the
// error of each command on that command and keeps reading, so the connection's
// EncodeDecode returns nil and the READONLY reply is only reported afterwards,
// wrapped in resp.ErrConnUsable, where the pool no longer looks at it. The
// pipeline is how counters are written (INCRBY and EXPIRE), so without this a
// demoted master keeps being used until the process restarts.
//
// This mirrors radix v4's unexported pipeline type and must be kept in step
// with it when radix is upgraded.
type readOnlyPipeline struct {
	actions    []radix.Action
	properties radix.ActionProperties
}

var _ radix.Action = (*readOnlyPipeline)(nil)

func newReadOnlyPipeline() *readOnlyPipeline {
	return &readOnlyPipeline{
		properties: radix.ActionProperties{
			CanPipeline:  true,
			CanShareConn: true,
		},
	}
}

// Append adds an Action to the pipeline. Like radix's pipeline, it panics if
// the Action cannot be pipelined.
func (p *readOnlyPipeline) Append(a radix.Action) {
	props := a.Properties()
	if !props.CanPipeline {
		panic(fmt.Sprintf("can't pipeline Action of type %T: %+v", a, a))
	}
	p.properties.Keys = append(p.properties.Keys, props.Keys...)
	p.properties.CanShareConn = p.properties.CanShareConn && props.CanShareConn
	p.actions = append(p.actions, a)
}

func (p *readOnlyPipeline) Properties() radix.ActionProperties {
	return p.properties
}

func (p *readOnlyPipeline) Perform(ctx context.Context, c radix.Conn) error {
	rec := &pipelineRecorder{Conn: c}
	for _, a := range p.actions {
		// Each Action only calls EncodeDecode, which rec records instead of
		// sending, so nothing is written yet.
		if err := a.Perform(ctx, rec); err != nil {
			return resp.ErrConnUsable{Err: err}
		}
	}

	// Write every command in one go and read every reply. Only errors that
	// leave the connection unusable (including READONLY) come back here.
	if err := c.EncodeDecode(ctx, rec, rec); err != nil {
		return err
	}

	// The remaining per-command errors leave the connection usable. Report the
	// first one the way radix does.
	for _, m := range rec.mm {
		if m.err != nil {
			return fmt.Errorf("command %+v in pipeline returned error: %w", m.marshal, m.err)
		}
	}
	return nil
}

type pipelineItem struct {
	marshal, unmarshalInto interface{}
	err                    error
}

// pipelineRecorder collects the EncodeDecode calls of the pipelined Actions and
// plays them back as one write and one read.
type pipelineRecorder struct {
	radix.Conn
	mm []pipelineItem
}

func (r *pipelineRecorder) EncodeDecode(_ context.Context, m, u interface{}) error {
	r.mm = append(r.mm, pipelineItem{marshal: m, unmarshalInto: u})
	return nil
}

func (r *pipelineRecorder) MarshalRESP(w io.Writer, o *resp.Opts) error {
	for i := range r.mm {
		if r.mm[i].marshal == nil {
			continue
		}
		err := resp3.Marshal(w, r.mm[i].marshal, o)
		switch {
		case err == nil:
		case isConnUsable(err):
			// Nothing was written for this command: remember its error and
			// carry on with the rest.
			r.mm[i].err = err
		default:
			return err
		}
	}
	return nil
}

func (r *pipelineRecorder) UnmarshalRESP(br resp.BufferedReader, o *resp.Opts) error {
	for i := range r.mm {
		if r.mm[i].unmarshalInto == nil || r.mm[i].err != nil {
			continue
		}
		err := resp3.Unmarshal(br, r.mm[i].unmarshalInto, o)
		switch {
		case err == nil:
		case isReadOnlyError(err):
			// The server is a replica. Stop reading and make the connection
			// unusable, so the pool discards it instead of reusing it.
			return resp.ErrConnUnusable(err)
		case isConnUsable(err):
			r.mm[i].err = err
		default:
			return err
		}
	}
	return nil
}

func isConnUsable(err error) bool {
	var usable resp.ErrConnUsable
	return errors.As(err, &usable)
}
