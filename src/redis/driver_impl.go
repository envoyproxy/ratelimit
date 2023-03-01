package redis

import (
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v3"
	"github.com/mediocregopher/radix/v3/trace"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/utils"
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

func poolTrace(ps *poolStats, healthCheckActiveConnection bool, srv server.Server) trace.PoolTrace {
	return trace.PoolTrace{
		ConnCreated: func(newConn trace.PoolConnCreated) {
			if newConn.Err == nil {
				ps.connectionTotal.Add(1)
				ps.connectionActive.Add(1)
				if healthCheckActiveConnection && srv != nil {
					err := srv.HealthChecker().Ok(server.RedisHealthComponentName)
					if err != nil {
						logger.Errorf("Unable to update health status: %s", err)
					}
				}
			} else {
				fmt.Println("creating redis connection error :", newConn.Err)
			}
		},
		ConnClosed: func(_ trace.PoolConnClosed) {
			ps.connectionActive.Sub(1)
			ps.connectionClose.Add(1)
			if healthCheckActiveConnection && srv != nil && ps.connectionActive.Value() == 0 {
				err := srv.HealthChecker().Fail(server.RedisHealthComponentName)
				if err != nil {
					logger.Errorf("Unable to update health status: %s", err)
				}
			}
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

func NewClientImpl(scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server) Client {
	maskedUrl := utils.MaskCredentialsInUrl(url)
	logger.Warnf("connecting to redis on %s with pool size %d", maskedUrl, poolSize)

	df := func(network, addr string) (radix.Conn, error) {
		var dialOpts []radix.DialOpt

		if useTls {
			dialOpts = append(dialOpts, radix.DialUseTLS(tlsConfig))
		}

		if auth != "" {
			user, pass, found := strings.Cut(auth, ":")
			if found {
				logger.Warnf("enabling authentication to redis on %s with user %s", maskedUrl, user)
				dialOpts = append(dialOpts, radix.DialAuthUser(user, pass))
			} else {
				logger.Warnf("enabling authentication to redis on %s without user", maskedUrl)
				dialOpts = append(dialOpts, radix.DialAuthPass(auth))
			}
		}

		return radix.Dial(network, addr, dialOpts...)
	}

	stats := newPoolStats(scope)

	opts := []radix.PoolOpt{radix.PoolConnFunc(df), radix.PoolWithTrace(poolTrace(&stats, healthCheckActiveConnection, srv))}

	implicitPipelining := true
	if pipelineWindow == 0 && pipelineLimit == 0 {
		implicitPipelining = false
	} else {
		opts = append(opts, radix.PoolPipelineWindow(pipelineWindow, pipelineLimit))
	}
	logger.Debugf("Implicit pipelining enabled: %v", implicitPipelining)

	poolFunc := func(network, addr string) (radix.Client, error) {
		return radix.NewPool(network, addr, poolSize, opts...)
	}

	var client radix.Client
	var err error
	switch strings.ToLower(redisType) {
	case "single":
		client, err = poolFunc(redisSocketType, url)
	case "cluster":
		urls := strings.Split(url, ",")
		if implicitPipelining == false {
			panic(RedisError("Implicit Pipelining must be enabled to work with Redis Cluster Mode. Set values for REDIS_PIPELINE_WINDOW or REDIS_PIPELINE_LIMIT to enable implicit pipelining"))
		}
		logger.Warnf("Creating cluster with urls %v", urls)
		client, err = radix.NewCluster(urls, radix.ClusterPoolFunc(poolFunc))
	case "sentinel":
		urls := strings.Split(url, ",")
		if len(urls) < 2 {
			panic(RedisError("Expected master name and a list of urls for the sentinels, in the format: <redis master name>,<sentinel1>,...,<sentineln>"))
		}
		client, err = radix.NewSentinel(urls[0], urls[1:], radix.SentinelPoolFunc(poolFunc))
	default:
		panic(RedisError("Unrecognized redis type " + redisType))
	}

	checkError(err)

	// Check if connection is good
	var pingResponse string
	checkError(client.Do(radix.Cmd(&pingResponse, "PING")))
	if pingResponse != "PONG" {
		checkError(fmt.Errorf("connecting redis error: %s", pingResponse))
	}

	return &clientImpl{
		client:             client,
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
