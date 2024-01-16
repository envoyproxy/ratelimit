package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/trace"
	logger "github.com/sirupsen/logrus"
	"strings"

	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type commonClient interface {
	// Do performs an Action on a Conn from a primary instance.
	Do(context.Context, radix.Action) error
	// Once Close() is called all future method calls on the Client will return
	// an error
	Close() error
}

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
	client             commonClient
	stats              poolStats
	implicitPipelining bool
}

func checkError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}

func NewClientImpl(ctx context.Context, scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	implicitPipelining bool, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server) Client {
	maskedUrl := utils.MaskCredentialsInUrl(url)
	logger.Warnf("connecting to redis on %s with pool size %d", maskedUrl, poolSize)

	stats := newPoolStats(scope)

	logger.Debugf("Implicit pipelining enabled: %v", implicitPipelining)

	poolConfig := radix.PoolConfig{Size: poolSize, Trace: poolTrace(&stats, healthCheckActiveConnection, srv)}
	if auth != "" {
		user, pass, found := strings.Cut(auth, ":")
		if found {
			logger.Warnf("enabling authentication to redis on %s with user %s", maskedUrl, user)
		} else {
			logger.Warnf("enabling authentication to redis on %s without user", maskedUrl)
			pass = user
			user = ""
		}

		poolConfig.Dialer = radix.Dialer{AuthUser: user, AuthPass: pass}
	}

	if useTls {
		poolConfig.Dialer.NetDialer = &tls.Dialer{Config: tlsConfig}
	}

	var client commonClient
	var err error
	switch strings.ToLower(redisType) {
	case "single":
		client, err = poolConfig.New(ctx, redisSocketType, url)
	case "cluster":
		urls := strings.Split(url, ",")
		if !implicitPipelining {
			panic(RedisError("Implicit Pipelining must be enabled to work with Redis Cluster Mode. Set values for REDIS_PIPELINE_WINDOW or REDIS_PIPELINE_LIMIT to enable implicit pipelining"))
		}
		logger.Warnf("Creating cluster with urls %v", urls)
		client, err = radix.ClusterConfig{PoolConfig: poolConfig}.New(ctx, urls)
	case "sentinel":
		urls := strings.Split(url, ",")
		if len(urls) < 2 {
			panic(RedisError("Expected master name and a list of urls for the sentinels, in the format: <redis master name>,<sentinel1>,...,<sentineln>"))
		}
		client, err = radix.SentinelConfig{PoolConfig: poolConfig}.New(ctx, urls[0], urls[1:])
	default:
		panic(RedisError("Unrecognized redis type " + redisType))
	}

	checkError(err)

	// Check if connection is good
	var pingResponse string
	checkError(client.Do(ctx, radix.Cmd(&pingResponse, "PING")))
	if pingResponse != "PONG" {
		checkError(fmt.Errorf("connecting redis error: %s", pingResponse))
	}

	return &clientImpl{
		client:             client,
		stats:              stats,
		implicitPipelining: implicitPipelining,
	}
}

func (c *clientImpl) DoCmd(ctx context.Context, rcv interface{}, cmd string, args ...interface{}) error {
	return c.client.Do(ctx, radix.FlatCmd(rcv, cmd, args...))
}

func (c *clientImpl) Close() error {
	return c.client.Close()
}

func (c *clientImpl) NumActiveConns() int {
	return int(c.stats.connectionActive.Value())
}

func (c *clientImpl) PipeAppend(pipeline Pipeline, rcv interface{}, cmd string, args ...interface{}) Pipeline {
	return append(pipeline, radix.FlatCmd(rcv, cmd, args...))
}

func (c *clientImpl) PipeDo(ctx context.Context, pipeline Pipeline) error {
	if c.implicitPipelining {
		for _, action := range pipeline {
			if err := c.client.Do(ctx, action); err != nil {
				return err
			}
		}
		return nil
	}

	newPipeline := radix.NewPipeline()
	for _, action := range pipeline {
		newPipeline.Append(action)
	}

	return c.client.Do(ctx, newPipeline)
}

func (c *clientImpl) ImplicitPipeliningEnabled() bool {
	return c.implicitPipelining
}
