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
	// @param args supplies the additional arguments.
	DoCmd(ctx context.Context, rcv interface{}, cmd string, args ...interface{}) error

	// PipeAppend append a command onto the pipeline queue.
	//
	// @param pipeline supplies the queue for pending commands.
	// @param rcv supplies receiver for the result.
	// @param cmd supplies the command to append.
	// @param args supplies the additional arguments.
	PipeAppend(pipeline Pipeline, rcv interface{}, cmd string, args ...interface{}) Pipeline

	// PipeScriptAppend append a script command onto the pipeline queue.
	//
	// @param pipeline supplies the queue for pending commands.
	// @param rcv supplies receiver for the result.
	// @param script supplies the script to append.
	// @param args supplies the additional arguments.
	PipeScriptAppend(pipeline Pipeline, rcv interface{}, script radix.EvalScript, args ...string) Pipeline

	// PipeDo writes multiple commands to a Conn in
	// a single write, then reads their responses in a single read. This reduces
	// network delay into a single round-trip.
	//
	// @param pipeline supplies the queue for pending commands.
	PipeDo(ctx context.Context, pipeline Pipeline) error

	// Once Close() is called all future method calls on the Client will return
	// an error
	Close() error

	// NumActiveConns return number of active connections, used in testing.
	NumActiveConns() int

	// ImplicitPipeliningEnabled return true if implicit pipelining is enabled.
	ImplicitPipeliningEnabled() bool
}

type Pipeline []radix.Action
