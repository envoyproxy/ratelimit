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
	client    redisClient
	stats     poolStats
	isCluster bool
}

func checkError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}

// createDialer creates a radix.Dialer with timeout, TLS, and auth configuration
// targetName is used for logging to identify the connection target (e.g., URL, "sentinel(url)")
func createDialer(timeout time.Duration, useTls bool, tlsConfig *tls.Config, auth string, targetName string) radix.Dialer {
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
		if targetName != "" {
			logger.Warnf("enabling TLS to redis %s", targetName)
		}
	}

	// Setup auth if provided
	if auth != "" {
		user, pass, found := strings.Cut(auth, ":")
		if found {
			logger.Warnf("enabling authentication to redis %s with user %s", targetName, user)
			dialer.AuthUser = user
			dialer.AuthPass = pass
		} else {
			logger.Warnf("enabling authentication to redis %s without user", targetName)
			dialer.AuthPass = auth
		}
	}

	return dialer
}

func NewClientImpl(scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server,
	timeout time.Duration, poolOnEmptyBehavior string, sentinelAuth string,
) Client {
	maskedUrl := utils.MaskCredentialsInUrl(url)
	logger.Warnf("connecting to redis on %s with pool size %d", maskedUrl, poolSize)

	// Create Dialer for connecting to Redis
	dialer := createDialer(timeout, useTls, tlsConfig, auth, maskedUrl)

	stats := newPoolStats(scope)

	// Create PoolConfig
	poolConfig := radix.PoolConfig{
		Dialer: dialer,
		Size:   poolSize,
		Trace:  poolTrace(&stats, healthCheckActiveConnection, srv),
	}

	// Determine pipeline mode based on Redis type:
	// - Cluster: uses grouped pipeline (same-key commands batched together)
	// - Single/Sentinel: uses explicit pipeline (all commands batched together)
	isCluster := strings.ToLower(redisType) == "cluster"

	// pipelineLimit parameter is deprecated and ignored in radix v4.
	if pipelineLimit > 0 {
		logger.Warnf("REDIS_PIPELINE_LIMIT=%d is deprecated and has no effect in radix v4. Write buffering is controlled solely by REDIS_PIPELINE_WINDOW.", pipelineLimit)
	}

	// Set WriteFlushInterval for cluster mode (grouped pipeline uses auto buffering)
	if isCluster && pipelineWindow > 0 {
		poolConfig.Dialer.WriteFlushInterval = pipelineWindow
		logger.Debugf("Cluster mode: setting WriteFlushInterval to %v", pipelineWindow)
	}

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

		// Create sentinel dialer (may use different auth from Redis master/replica)
		// sentinelAuth is for Sentinel nodes, auth is for Redis master/replica
		sentinelDialer := createDialer(timeout, useTls, tlsConfig, sentinelAuth, fmt.Sprintf("sentinel(%s)", maskedUrl))

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
		client:    client,
		stats:     stats,
		isCluster: isCluster,
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
	return append(pipeline, PipelineAction{
		Action: radix.FlatCmd(rcv, cmd, allArgs...),
		Key:    key,
	})
}

func (c *clientImpl) PipeDo(pipeline Pipeline) error {
	ctx := context.Background()
	if c.isCluster {
		// Cluster mode: group commands by key and execute each group as a pipeline.
		// This ensures INCRBY + EXPIRE for the same key are pipelined together (same slot),
		// reducing round-trips from 2 to 1 per key.
		return c.executeGroupedPipeline(ctx, pipeline)
	}

	// Single/Sentinel mode: batch all commands in a single pipeline.
	p := radix.NewPipeline()
	for _, pipelineAction := range pipeline {
		p.Append(pipelineAction.Action)
	}
	return c.client.Do(ctx, p)
}

// executeGroupedPipeline groups pipeline actions by key and executes each group
// as a separate pipeline. This allows same-key commands (like INCRBY + EXPIRE)
// to be pipelined together even in cluster mode.
func (c *clientImpl) executeGroupedPipeline(ctx context.Context, pipeline Pipeline) error {
	// Group actions by key, preserving first-occurrence order
	var groups [][]radix.Action
	keyToIndex := make(map[string]int)

	for _, pa := range pipeline {
		if idx, exists := keyToIndex[pa.Key]; exists {
			groups[idx] = append(groups[idx], pa.Action)
		} else {
			keyToIndex[pa.Key] = len(groups)
			groups = append(groups, []radix.Action{pa.Action})
		}
	}

	// Execute each group
	for _, actions := range groups {
		if len(actions) == 1 {
			if err := c.client.Do(ctx, actions[0]); err != nil {
				return err
			}
		} else {
			// Multiple commands for same key: pipeline them together
			p := radix.NewPipeline()
			for _, action := range actions {
				p.Append(action)
			}
			if err := c.client.Do(ctx, p); err != nil {
				return err
			}
		}
	}

	return nil
}
