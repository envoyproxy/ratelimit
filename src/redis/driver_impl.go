package redis

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/mediocregopher/radix/v3/trace"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v3"
	logger "github.com/sirupsen/logrus"
)

type poolStats struct {
	connectionActive stats.Gauge
	connectionTotal  stats.Counter
	connectionClose  stats.Counter
}

func newPoolStats(scope stats.Scope) poolStats {
	ret := poolStats{}
	ret.connectionActive = scope.NewGauge("cx_active")
	ret.connectionTotal = scope.NewCounter("cx_total")
	ret.connectionClose = scope.NewCounter("cx_local_close")
	return ret
}

func poolTrace(ps *poolStats) trace.PoolTrace {
	return trace.PoolTrace{
		ConnCreated: func(_ trace.PoolConnCreated) {
			ps.connectionTotal.Add(1)
			ps.connectionActive.Add(1)
		},
		ConnClosed: func(_ trace.PoolConnClosed) {
			ps.connectionActive.Sub(1)
			ps.connectionClose.Add(1)
		},
	}
}

type clientImpl struct {
	client             radix.Client
	stats              poolStats
	implicitPipelining bool
}

func checkError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}

func NewClientImpl(scope stats.Scope, useTls bool, auth string, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int) Client {
	logger.Warnf("connecting to redis on %s with pool size %d", url, poolSize)

	df := func(network, addr string) (radix.Conn, error) {
		var dialOpts []radix.DialOpt

		var err error
		if useTls {
			dialOpts = append(dialOpts, radix.DialUseTLS(&tls.Config{}))
		}

		if err != nil {
			return nil, err
		}
		if auth != "" {
			logger.Warnf("enabling authentication to redis on %s", url)

			dialOpts = append(dialOpts, radix.DialAuthPass(auth))
		}

		return radix.Dial(network, addr, dialOpts...)
	}

	stats := newPoolStats(scope)

	opts := []radix.PoolOpt{radix.PoolConnFunc(df), radix.PoolWithTrace(poolTrace(&stats))}

	implicitPipelining := true
	if pipelineWindow == 0 && pipelineLimit == 0 {
		implicitPipelining = false
	} else {
		opts = append(opts, radix.PoolPipelineWindow(pipelineWindow, pipelineLimit))
	}

	// TODO: support sentinel and redis cluster
	pool, err := radix.NewPool("tcp", url, poolSize, opts...)
	checkError(err)

	// Check if connection is good
	var pingResponse string
	checkError(pool.Do(radix.Cmd(&pingResponse, "PING")))
	if pingResponse != "PONG" {
		checkError(fmt.Errorf("connecting redis error: %s", pingResponse))
	}

	return &clientImpl{
		client:             pool,
		stats:              stats,
		implicitPipelining: implicitPipelining,
	}
}

func (c *clientImpl) DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error {
	return c.client.Do(radix.FlatCmd(rcv, cmd, key, args...))
}

func (c *clientImpl) Close() error {
	return c.client.Close()
}

func (c *clientImpl) NumActiveConns() int {
	return int(c.stats.connectionActive.Value())
}

func (c *clientImpl) PipeAppend(pipeline Pipeline, rcv interface{}, cmd, key string, args ...interface{}) Pipeline {
	return append(pipeline, radix.FlatCmd(rcv, cmd, key, args...))
}

func (c *clientImpl) PipeDo(pipeline Pipeline) error {
	if c.implicitPipelining {
		for _, action := range pipeline {
			if err := c.client.Do(action); err != nil {
				return err
			}
		}
		return nil
	}

	return c.client.Do(radix.Pipeline(pipeline...))
}

func (c *clientImpl) ImplicitPipeliningEnabled() bool {
	return c.implicitPipelining
}
