package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/trace"
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
				logger.Errorf("creating redis connection error : %v", newConn.Err)
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

// redisClient is an interface that abstracts radix Client, Cluster, and Sentinel
// All of these types have Do(context.Context, Action) and Close() methods
type redisClient interface {
	Do(context.Context, radix.Action) error
	Close() error
}

type clientImpl struct {
	client              redisClient
	stats               poolStats
	useExplicitPipeline bool
}

func checkError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}

func NewClientImpl(scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server,
	timeout time.Duration, poolOnEmptyBehavior string, poolOnEmptyWaitDuration time.Duration, sentinelAuth string,
	useExplicitPipeline bool,
) Client {
	maskedUrl := utils.MaskCredentialsInUrl(url)
	logger.Warnf("connecting to redis on %s with pool size %d", maskedUrl, poolSize)

	// Create Dialer for connecting to Redis
	var netDialer net.Dialer
	if timeout > 0 {
		netDialer.Timeout = timeout
	}

	dialer := radix.Dialer{
		NetDialer: &netDialer,
	}

	// Setup TLS if needed
	if useTls {
		tlsNetDialer := tls.Dialer{
			NetDialer: &netDialer,
			Config:    tlsConfig,
		}
		dialer.NetDialer = &tlsNetDialer
	}

	if auth != "" {
		user, pass, found := strings.Cut(auth, ":")
		if found {
			logger.Warnf("enabling authentication to redis on %s with user %s", maskedUrl, user)
			dialer.AuthUser = user
			dialer.AuthPass = pass
		} else {
			logger.Warnf("enabling authentication to redis on %s without user", maskedUrl)
			dialer.AuthPass = auth
		}
	}

	stats := newPoolStats(scope)

	// Create PoolConfig
	poolConfig := radix.PoolConfig{
		Dialer: dialer,
		Size:   poolSize,
		Trace:  poolTrace(&stats, healthCheckActiveConnection, srv),
	}

	// Note: radix v4 handles write buffering via WriteFlushInterval.
	// Explicit pipelining (radix.NewPipeline()) is only used when explicitly requested via useExplicitPipeline parameter.
	// Otherwise, individual Do() calls are used with automatic write buffering via WriteFlushInterval.
	// pipelineLimit parameter is deprecated and ignored in radix v4.

	// Warn if deprecated pipelineLimit is set
	if pipelineLimit > 0 {
		logger.Warnf("REDIS_PIPELINE_LIMIT=%d is deprecated and has no effect in radix v4. Write buffering is controlled solely by REDIS_PIPELINE_WINDOW.", pipelineLimit)
	}

	// Set WriteFlushInterval for automatic write buffering when not using explicit pipeline
	if !useExplicitPipeline && pipelineWindow > 0 {
		poolConfig.Dialer.WriteFlushInterval = pipelineWindow
		logger.Debugf("Setting WriteFlushInterval to %v", pipelineWindow)
	}

	logger.Debugf("Use explicit pipeline: %v", useExplicitPipeline)

	// IMPORTANT: radix v4 pool behavior changes from v3
	//
	// v4 uses a FIXED pool size and BLOCKS when all connections are in use.
	// This is the same as v3's WAIT behavior.
	//
	// v3 CREATE and ERROR behaviors are NOT supported in v4:
	// - v3 WAIT   → v4 supported (blocks until connection available)
	// - v3 CREATE → v4 NOT SUPPORTED (would block instead of creating overflow connections)
	// - v3 ERROR  → v4 NOT SUPPORTED (would block instead of failing fast)
	//
	// Migration requirements:
	// - Remove REDIS_POOL_ON_EMPTY_BEHAVIOR setting if set to CREATE or ERROR
	// - Use WAIT or leave unset (WAIT is default)
	// - Consider increasing REDIS_POOL_SIZE if you previously relied on CREATE
	// - Use context timeouts to prevent indefinite blocking:
	//     ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	//     defer cancel()
	//     client.Do(ctx, cmd)
	switch strings.ToUpper(poolOnEmptyBehavior) {
	case "WAIT":
		logger.Warnf("Redis pool %s: WAIT is default in radix v4 (blocks until connection available)", maskedUrl)
	case "CREATE":
		// v3 CREATE created overflow connections when pool was full
		// v4 does NOT support this - fail fast to prevent unexpected blocking behavior
		panic(RedisError("REDIS_POOL_ON_EMPTY_BEHAVIOR=CREATE is not supported in radix v4. Pool will block instead of creating overflow connections. Remove this setting or set to WAIT, and consider increasing REDIS_POOL_SIZE."))
	case "ERROR":
		// v3 ERROR failed fast when pool was full
		// v4 does NOT support this - fail fast to prevent unexpected blocking behavior
		panic(RedisError("REDIS_POOL_ON_EMPTY_BEHAVIOR=ERROR is not supported in radix v4. Pool will block instead of failing fast. Remove this setting or set to WAIT, and use context timeouts for fail-fast behavior."))
	default:
		logger.Warnf("Redis pool %s: using v4 default (fixed size=%d, blocks when full)", maskedUrl, poolSize)
	}

	poolFunc := func(ctx context.Context, network, addr string) (radix.Client, error) {
		return poolConfig.New(ctx, network, addr)
	}

	var client redisClient
	var err error
	ctx := context.Background()

	switch strings.ToLower(redisType) {
	case "single":
		logger.Warnf("Creating single with urls %v", url)
		client, err = poolFunc(ctx, redisSocketType, url)
	case "cluster":
		urls := strings.Split(url, ",")
		if useExplicitPipeline {
			panic(RedisError("Explicit pipelining cannot be used with Redis Cluster Mode. Set REDIS_PIPELINE_WINDOW to a non-zero value (e.g., 150us)"))
		}
		logger.Warnf("Creating cluster with urls %v", urls)
		clusterConfig := radix.ClusterConfig{
			PoolConfig: poolConfig,
		}
		client, err = clusterConfig.New(ctx, urls)
	case "sentinel":
		urls := strings.Split(url, ",")
		if len(urls) < 2 {
			panic(RedisError("Expected master name and a list of urls for the sentinels, in the format: <redis master name>,<sentinel1>,...,<sentineln>"))
		}

		// Create sentinel dialer
		var sentinelNetDialer net.Dialer
		if timeout > 0 {
			sentinelNetDialer.Timeout = timeout
		}

		sentinelDialer := radix.Dialer{
			NetDialer: &sentinelNetDialer,
		}

		// Setup TLS for sentinel if needed
		if useTls {
			logger.Warnf("enabling TLS to redis sentinel")
			tlsSentinelDialer := tls.Dialer{
				NetDialer: &sentinelNetDialer,
				Config:    tlsConfig,
			}
			sentinelDialer.NetDialer = &tlsSentinelDialer
		}

		// Use sentinelAuth for authenticating to Sentinel nodes, not auth
		// auth is used for Redis master/replica authentication
		if sentinelAuth != "" {
			user, pass, found := strings.Cut(sentinelAuth, ":")
			if found {
				logger.Warnf("enabling authentication to redis sentinel with user %s", user)
				sentinelDialer.AuthUser = user
				sentinelDialer.AuthPass = pass
			} else {
				logger.Warnf("enabling authentication to redis sentinel without user")
				sentinelDialer.AuthPass = sentinelAuth
			}
		}

		sentinelConfig := radix.SentinelConfig{
			PoolConfig:     poolConfig,
			SentinelDialer: sentinelDialer,
		}
		client, err = sentinelConfig.New(ctx, urls[0], urls[1:])
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
		client:              client,
		stats:               stats,
		useExplicitPipeline: useExplicitPipeline,
	}
}

func (c *clientImpl) DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error {
	ctx := context.Background()
	// Combine key and args into a single slice
	allArgs := make([]interface{}, 0, 1+len(args))
	allArgs = append(allArgs, key)
	allArgs = append(allArgs, args...)
	return c.client.Do(ctx, radix.FlatCmd(rcv, cmd, allArgs...))
}

func (c *clientImpl) Close() error {
	return c.client.Close()
}

func (c *clientImpl) NumActiveConns() int {
	return int(c.stats.connectionActive.Value())
}

func (c *clientImpl) PipeAppend(pipeline Pipeline, rcv interface{}, cmd, key string, args ...interface{}) Pipeline {
	// Combine key and args into a single slice
	allArgs := make([]interface{}, 0, 1+len(args))
	allArgs = append(allArgs, key)
	allArgs = append(allArgs, args...)
	return append(pipeline, radix.FlatCmd(rcv, cmd, allArgs...))
}

func (c *clientImpl) PipeDo(pipeline Pipeline) error {
	ctx := context.Background()
	if c.useExplicitPipeline {
		// When explicit pipelining is enabled (WriteFlushInterval == 0):
		// Use radix.NewPipeline() to batch commands together.
		p := radix.NewPipeline()
		for _, action := range pipeline {
			p.Append(action)
		}
		return c.client.Do(ctx, p)
	}

	// When automatic buffering is enabled (WriteFlushInterval > 0):
	// Execute each action individually. Radix v4 will automatically buffer
	// concurrent writes and flush them together based on WriteFlushInterval.
	// This provides better performance for most workloads.
	for _, action := range pipeline {
		if err := c.client.Do(ctx, action); err != nil {
			return err
		}
	}
	return nil
}

func (c *clientImpl) UseExplicitPipeline() bool {
	return c.useExplicitPipeline
}
