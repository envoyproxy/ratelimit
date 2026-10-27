package redis

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mediocregopher/radix/v4"
)

var errPoolWaitTimeout = errors.New("redis connection pool wait timeout")

var errPoolClosed = errors.New("redis connection pool is closed")

// exclusiveRedisAction owns its connection, so cancellation can interrupt both
// reads and writes without closing a peer request's connection. Routing and
// redirect-retry properties are preserved.
type exclusiveRedisAction struct{ radix.Action }

func (a exclusiveRedisAction) Properties() radix.ActionProperties {
	properties := a.Action.Properties()
	properties.CanShareConn = false
	return properties
}

func (a exclusiveRedisAction) Perform(ctx context.Context, conn radix.Conn) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ctx.Done() == nil {
		return a.Action.Perform(ctx, conn)
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(closed)
		_ = conn.Close()
	})
	defer func() {
		if !stop() {
			// Do not return a connection to the pool while its cancellation
			// callback can still close it. Report cancellation so the pool
			// discards the connection even if a reply raced with its closure.
			<-closed
			err = ctx.Err()
		}
	}()
	return a.Action.Perform(ctx, conn)
}

// poolWaitAction switches back to the caller's context after acquisition. The
// queue timer must not shorten the lifetime of an already-admitted operation.
type poolWaitAction struct {
	radix.Action
	requestContext context.Context
	stopWaiting    context.CancelFunc
	acquired       bool
}

func (a *poolWaitAction) Perform(waitContext context.Context, conn radix.Conn) error {
	if err := a.requestContext.Err(); err != nil {
		return err
	}
	if err := waitContext.Err(); err != nil {
		return err
	}
	a.acquired = true
	a.stopWaiting()
	return a.Action.Perform(a.requestContext, conn)
}

// exclusivePool also owns cluster topology and explicit control commands.
// A nonzero I/O timeout bounds acquisition separately from active socket I/O,
// preventing a burst from waiting through successive reconnect attempts.
type exclusivePool struct {
	radix.Client
	waitTimeout time.Duration
	lifetime    context.Context
	stop        context.CancelFunc
	mu          sync.Mutex
	closed      bool
	actions     sync.WaitGroup
	closeDone   chan struct{}
	healthDone  chan struct{}
}

func (p *exclusivePool) Do(ctx context.Context, action radix.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errPoolClosed
	}
	p.actions.Add(1)
	p.mu.Unlock()
	defer p.actions.Done()
	ctx, requestCancel := context.WithCancel(ctx)
	shutdown := make(chan struct{})
	stopShutdown := context.AfterFunc(p.lifetime, func() {
		requestCancel()
		close(shutdown)
	})
	defer func() {
		requestCancel()
		if !stopShutdown() {
			<-shutdown
		}
	}()
	if _, owned := action.(exclusiveRedisAction); !owned {
		action = exclusiveRedisAction{action}
	}
	if p.waitTimeout <= 0 {
		return p.Client.Do(ctx, action)
	}
	waitContext, waitCancel := context.WithTimeout(ctx, p.waitTimeout)
	defer waitCancel()
	queued := &poolWaitAction{Action: action, requestContext: ctx, stopWaiting: waitCancel}
	err := p.Client.Do(waitContext, queued)
	if !queued.acquired && err != nil && ctx.Err() == nil && errors.Is(waitContext.Err(), context.DeadlineExceeded) {
		return errPoolWaitTimeout
	}
	return err
}

func (p *exclusivePool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.closeDone
		return errPoolClosed
	}
	p.closed = true
	p.stop()
	p.mu.Unlock()
	defer close(p.closeDone)
	// The health loop uses Do too. Stop and join it before waiting for the
	// remaining actions, so it cannot start a new probe during pool shutdown.
	<-p.healthDone
	// Radix's raw pool only closes idle connections. Cancel and join owned
	// actions first, while their discard paths can still update pool traces.
	p.actions.Wait()
	return p.Client.Close()
}

func (p *exclusivePool) checkHealth(interval time.Duration, poolSize int) {
	defer close(p.healthDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.lifetime.Done():
			return
		case <-ticker.C:
			// One failed probe can leave other stale connections in the pool.
			// Give the remaining slots a read-only check without waiting for
			// another tick each time. The whole sweep shares Radix's two-second
			// probe deadline, even when application I/O timeouts are disabled.
			// Each Do owns only its probe's connection; mutations never replay.
			ctx, cancel := context.WithTimeout(p.lifetime, 2*time.Second)
			if err := p.Do(ctx, radix.Cmd(nil, "PING")); err != nil {
				for checked := 1; checked < poolSize && ctx.Err() == nil; checked++ {
					_ = p.Do(ctx, radix.Cmd(nil, "PING"))
				}
				// Do not consume a tick buffered during a long sweep and start
				// another recovery sweep immediately.
				ticker.Reset(interval)
			}
			cancel()
		}
	}
}

func exclusivePoolConfig(config radix.PoolConfig, waitTimeout time.Duration) radix.PoolConfig {
	// The built-in pinger uses the raw shared pool and holds its read-lock
	// during network I/O, bypassing this adapter. Preserve its cadence but
	// send probes through the same bounded ownership path as other commands.
	pingInterval := config.PingInterval
	size := config.Size
	if size == 0 {
		size = 4 // Radix's default pool size.
	}
	if pingInterval == 0 {
		pingInterval = 5 * time.Second / time.Duration(size)
	}
	config.PingInterval = -1
	base := config
	base.CustomPool = nil
	config.CustomPool = func(ctx context.Context, network, addr string) (radix.Client, error) {
		pool, err := base.New(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		lifetime, stop := context.WithCancel(context.Background())
		owned := &exclusivePool{Client: pool, waitTimeout: waitTimeout, lifetime: lifetime, stop: stop, closeDone: make(chan struct{}), healthDone: make(chan struct{})}
		if pingInterval > 0 {
			go owned.checkHealth(pingInterval, size)
		} else {
			close(owned.healthDone)
		}
		return owned, nil
	}
	return config
}
