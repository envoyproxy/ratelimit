package redis

import (
	"context"

	"github.com/mediocregopher/radix/v4"
)

// Errors that may be raised during config parsing.
type RedisError string

func (e RedisError) Error() string {
	return string(e)
}

// Interface for a redis client.
type Client interface {
	// DoCmd is used to perform a redis command and retrieve a result.
	//
	// @param rcv supplies receiver for the result.
	// @param cmd supplies the command to append.
	// @param key supplies the key to append.
	// @param args supplies the additional arguments.
	DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error

	// PipeAppend append a command onto the pipeline queue.
	//
	// @param pipeline supplies the queue for pending commands.
	// @param rcv supplies receiver for the result.
	// @param cmd supplies the command to append.
	// @param key supplies the key to append.
	// @param args supplies the additional arguments.
	PipeAppend(pipeline Pipeline, rcv interface{}, cmd, key string, args ...interface{}) Pipeline

	// PipeAppendWithRoutingKey appends a command onto the pipeline queue whose
	// Redis key differs from the command's first positional argument. This is
	// needed for commands like EVAL, where the first argument is the script body
	// and the actual key appears later in the argument list. In Redis Cluster
	// mode the routing key determines which slot/node the command is sent to, so
	// it must be the real key rather than the script text.
	//
	// @param pipeline supplies the queue for pending commands.
	// @param routingKey supplies the key used for cluster slot routing/grouping.
	// @param rcv supplies receiver for the result.
	// @param cmd supplies the command to append.
	// @param key supplies the first positional argument of the command.
	// @param args supplies the additional arguments.
	PipeAppendWithRoutingKey(pipeline Pipeline, routingKey string, rcv interface{}, cmd, key string, args ...interface{}) Pipeline

	// PipeDo writes multiple commands to a Conn in
	// a single write, then reads their responses in a single read. This reduces
	// network delay into a single round-trip.
	//
	// @param ctx supplies the context for Redis I/O.
	// @param pipeline supplies the queue for pending commands.
	PipeDo(ctx context.Context, pipeline Pipeline) error

	// Once Close() is called all future method calls on the Client will return
	// an error
	Close() error

	// NumActiveConns return number of active connections, used in testing.
	NumActiveConns() int
}

// PipelineAction represents a single action in the pipeline along with its key.
// The key is used for grouping commands in cluster mode.
type PipelineAction struct {
	Action radix.Action
	Key    string
}

type Pipeline []PipelineAction
