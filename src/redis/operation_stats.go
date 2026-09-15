package redis

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/resp/resp3"
)

type callOutcome int

const (
	callSuccess callOutcome = iota
	callCanceled
	callDeadline
	callRedisError
	callNetworkError
	callOtherError
	callOutcomeCount
)

type clientCallStats struct {
	started          stats.Counter
	completed        [callOutcomeCount]stats.Counter
	actionsAttempted stats.Counter
	inFlight         stats.Gauge
	duration         stats.Timer
}

type operationStats struct {
	command     clientCallStats
	pipeline    clientCallStats
	startupPing clientCallStats
}

func newOperationStats(scope stats.Scope) operationStats {
	scope = scope.Scope("client")
	return operationStats{
		command:     newClientCallStats(scope.Scope("command")),
		pipeline:    newClientCallStats(scope.Scope("pipeline")),
		startupPing: newClientCallStats(scope.Scope("startup_ping")),
	}
}

func newClientCallStats(scope stats.Scope) clientCallStats {
	s := clientCallStats{
		started:          scope.NewCounter("calls_started"),
		actionsAttempted: scope.NewCounter("actions_attempted"),
		inFlight:         scope.NewGauge("calls_in_flight"),
		duration:         scope.NewMilliTimer("call_duration"),
	}
	// Create all outcomes once. Neither command arguments nor error text may
	// become metric names or labels.
	for outcome, name := range [...]string{"success", "canceled", "deadline", "redis_error", "network_error", "other_error"} {
		s.completed[outcome] = scope.Scope("calls_completed").NewCounter(name)
	}
	return s
}

// do measures one synchronous Radix client call. actions is the number of
// planned action entries supplied to that call, not a count of commands sent
// over the network: the call may fail before writing, or Radix may retry it.
func (s *clientCallStats) do(ctx context.Context, client redisClient, action radix.Action, actions int) error {
	s.started.Inc()
	s.actionsAttempted.Add(uint64(actions))
	s.inFlight.Inc()
	defer s.inFlight.Dec()

	start := time.Now()
	err := client.Do(ctx, action)
	elapsed := time.Since(start)
	s.completed[classifyCallOutcome(err)].Inc()
	// AddDuration truncates to whole milliseconds in gostats. Preserve shorter
	// calls, and let the Prometheus mapper convert milliseconds to seconds.
	s.duration.AddValue(float64(elapsed) / float64(time.Millisecond))
	return err
}

func classifyCallOutcome(err error) callOutcome {
	switch {
	case err == nil:
		return callSuccess
	case errors.Is(err, context.Canceled):
		return callCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return callDeadline
	}

	var simpleError resp3.SimpleError
	var blobError resp3.BlobError
	var simpleErrorPointer *resp3.SimpleError
	var blobErrorPointer *resp3.BlobError
	if errors.As(err, &simpleError) || errors.As(err, &blobError) ||
		errors.As(err, &simpleErrorPointer) || errors.As(err, &blobErrorPointer) {
		return callRedisError
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return callNetworkError
	}
	return callOtherError
}
