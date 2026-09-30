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

type pipelineAction interface {
	radix.Action
	Append(radix.Action)
}

type readOnlyAwarePipeline struct {
	actions           []radix.Action
	actionsBuf        [8]radix.Action
	items             []pipelineItem
	itemsBuf          [8]pipelineItem
	properties        radix.ActionProperties
	propertiesKeysBuf [8]string
	radix.Conn
}

type pipelineItem struct {
	marshal       interface{}
	unmarshalInto interface{}
	err           error
}

func newReadOnlyAwarePipeline() *readOnlyAwarePipeline {
	pipeline := &readOnlyAwarePipeline{}
	pipeline.actions = pipeline.actionsBuf[:0]
	pipeline.items = pipeline.itemsBuf[:0]
	pipeline.properties = radix.ActionProperties{
		Keys:         pipeline.propertiesKeysBuf[:0],
		CanPipeline:  true,
		CanShareConn: true,
	}
	return pipeline
}

func (p *readOnlyAwarePipeline) Append(action radix.Action) {
	props := action.Properties()
	if !props.CanPipeline {
		panic(fmt.Sprintf("can't pipeline Action of type %T: %+v", action, action))
	}
	p.properties.Keys = append(p.properties.Keys, props.Keys...)
	p.properties.CanShareConn = p.properties.CanShareConn && props.CanShareConn
	p.actions = append(p.actions, action)
}

func (p *readOnlyAwarePipeline) Properties() radix.ActionProperties {
	return p.properties
}

func (p *readOnlyAwarePipeline) Perform(ctx context.Context, conn radix.Conn) error {
	p.Conn = conn
	defer func() { p.Conn = nil }()
	p.items = p.items[:0]

	for _, action := range p.actions {
		if err := action.Perform(ctx, p); err != nil {
			return resp.ErrConnUsable{Err: err}
		}
	}

	if err := conn.EncodeDecode(ctx, p, p); err != nil {
		return err
	}

	for _, item := range p.items {
		if item.err != nil {
			return fmt.Errorf("command %+v in pipeline returned error: %w", item.marshal, item.err)
		}
	}

	return nil
}

func (p *readOnlyAwarePipeline) EncodeDecode(_ context.Context, marshal, unmarshalInto interface{}) error {
	p.items = append(p.items, pipelineItem{
		marshal:       marshal,
		unmarshalInto: unmarshalInto,
	})
	return nil
}

func (p *readOnlyAwarePipeline) MarshalRESP(w io.Writer, opts *resp.Opts) error {
	for i := range p.items {
		if p.items[i].marshal == nil {
			continue
		}
		err := resp3.Marshal(w, p.items[i].marshal, opts)
		if err == nil {
			continue
		}
		if errors.As(err, new(resp.ErrConnUsable)) {
			p.items[i].err = err
			continue
		}
		return err
	}
	return nil
}

func (p *readOnlyAwarePipeline) UnmarshalRESP(br resp.BufferedReader, opts *resp.Opts) error {
	for i := range p.items {
		if p.items[i].unmarshalInto == nil || p.items[i].err != nil {
			continue
		}
		err := resp3.Unmarshal(br, p.items[i].unmarshalInto, opts)
		if err == nil {
			continue
		}
		if isReadOnlyError(err) {
			return resp.ErrConnUnusable(err)
		}
		if errors.As(err, new(resp.ErrConnUsable)) {
			p.items[i].err = err
			continue
		}
		return err
	}
	return nil
}
