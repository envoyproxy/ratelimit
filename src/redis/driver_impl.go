package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jpillora/backoff"
	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/trace"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/utils"
)

type poolStats struct {
	connectionActive       stats.Gauge
	connectionTotal        stats.Counter
	connectionClose        stats.Counter
	connectionCloseOnError stats.Counter
	connectionError        stats.Counter
	hadConnError           *atomic.Bool
	closeLogLimiter        *utils.LogLimiter

	// Redis commands (pipelines) issued by rate limit calls.
	cmdInflight         stats.Gauge
	cmdTotal            stats.Counter
	cmdSuccess          stats.Counter
	cmdError            stats.Counter
	cmdIoTimeout        stats.Counter
	cmdDeadlineExceeded stats.Counter
	cmdRejected         stats.Counter
	cmdTime             stats.Timer
}

func newPoolStats(scope stats.Scope) poolStats {
	ret := poolStats{}
	ret.connectionActive = scope.NewGauge("cx_active")
	ret.connectionTotal = scope.NewCounter("cx_total")
	ret.connectionClose = scope.NewCounter("cx_local_close")
	ret.connectionCloseOnError = scope.NewCounter("cx_local_close_error")
	ret.connectionError = scope.NewCounter("cx_connect_error")
	ret.hadConnError = new(atomic.Bool)
	ret.closeLogLimiter = utils.NewLogLimiter(10 * time.Second)

	ret.cmdInflight = scope.NewGauge("cmd_inflight")
	ret.cmdTotal = scope.NewCounter("cmd_total")
	ret.cmdSuccess = scope.NewCounter("cmd_success")
	ret.cmdError = scope.NewCounter("cmd_error")
	ret.cmdIoTimeout = scope.NewCounter("cmd_io_timeout")
	ret.cmdDeadlineExceeded = scope.NewCounter("cmd_deadline_exceeded")
	ret.cmdRejected = scope.NewCounter("cmd_rejected")
	ret.cmdTime = scope.NewTimer("cmd_time")
	return ret
}

// recordCmdTime records a command's duration in cmd_time. Stats sinks read
// timer values as milliseconds, but gostats timers created with NewTimer
// record whole microseconds and NewMilliTimer whole milliseconds, which
// would read most Redis commands, well under a millisecond, as 0. Record
// fractional milliseconds instead.
func (ps *poolStats) recordCmdTime(d time.Duration) {
	ps.cmdTime.AddValue(float64(d) / float64(time.Millisecond))
}

func poolTrace(ps *poolStats, healthCheckActiveConnection bool, srv server.Server) trace.PoolTrace {
	return trace.PoolTrace{
		ConnCreated: func(newConn trace.PoolConnCreated) {
			if newConn.Err == nil {
				ps.connectionTotal.Add(1)
				ps.connectionActive.Add(1)
				if ps.hadConnError.CompareAndSwap(true, false) {
					logger.Infof("redis connection re-established after previous error")
				}
				if healthCheckActiveConnection && srv != nil {
					err := srv.HealthChecker().Ok(server.RedisHealthComponentName)
					if err != nil {
						logger.Errorf("Unable to update health status: %s", err)
					}
				}
			} else {
				ps.hadConnError.Store(true)
				ps.connectionError.Inc()
				logger.Errorf("creating redis connection error : %v", newConn.Err)
			}
		},
		ConnClosed: func(closed trace.PoolConnClosed) {
			ps.connectionActive.Sub(1)
			ps.connectionClose.Add(1)
			if closed.Reason == trace.PoolConnClosedReasonError {
				ps.connectionCloseOnError.Inc()
				if ok, suppressed := ps.closeLogLimiter.Allow(); ok {
					logger.Warnf("redis connection to %s closed after an error, reconnecting (%d similar messages suppressed)", closed.Addr, suppressed)
				}
			}
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
	client                     redisClient
	dialPubSub                 func(context.Context) (radix.Conn, error)
	stats                      poolStats
	isCluster                  bool
	clusterPipelineParallelism int
	// inflight bounds the number of commands in flight, nil if unbounded.
	inflight chan struct{}
	// failWhenNoConnection makes commands fail right away while the pool has
	// no connection (REDIS_POOL_ON_EMPTY_BEHAVIOR=ERROR).
	failWhenNoConnection bool
	// ioTimeout is the I/O timeout of the connections, see clientOptions.
	ioTimeout time.Duration
	// stopProbe and probeDone stop the health probe, if any.
	stopProbe, probeDone chan struct{}
	stopOnce             sync.Once
}

func checkError(err error) {
	if err != nil {
		panic(RedisError(err.Error()))
	}
}

func effectiveClusterPipelineParallelism(configuredParallelism, poolSize int) int {
	if configuredParallelism < 0 {
		panic(RedisError("redis cluster pipeline parallelism must be >= 0"))
	}

	if configuredParallelism == 1 {
		return 1
	}

	poolCeiling := poolSize
	if poolCeiling < 1 {
		poolCeiling = 1
	}

	if configuredParallelism == 0 || configuredParallelism > poolCeiling {
		return poolCeiling
	}

	return configuredParallelism
}

// parseSentinelUrl splits a sentinel REDIS_URL of the form
// <redis master name>,<sentinel1>,...,<sentineln>.
func parseSentinelUrl(url string) (masterName string, sentinelAddrs []string, err error) {
	urls := strings.Split(url, ",")
	if len(urls) < 2 {
		return "", nil, fmt.Errorf("Expected master name and a list of urls for the sentinels, in the format: <redis master name>,<sentinel1>,...,<sentineln>")
	}
	return urls[0], urls[1:], nil
}

// createDialer creates a radix.Dialer with timeout, TLS, and auth configuration
// targetName is used for logging to identify the connection target (e.g., URL, "sentinel(url)")
// opts sets the I/O timeout and TCP options of the connections.
func createDialer(timeout time.Duration, opts clientOptions, useTls bool, tlsConfig *tls.Config, auth string, targetName string) radix.Dialer {
	dialer := radix.Dialer{
		NetDialer: newNetDialer(timeout, opts, useTls, tlsConfig),
	}

	if useTls && targetName != "" {
		logger.Warnf("enabling TLS to redis %s", targetName)
	}

	// Setup auth if provided
	if auth != "" {
		user, pass, found := splitAuth(auth)
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

func NewClientImpl(ctx context.Context, scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server,
	timeout time.Duration, poolOnEmptyBehavior string, sentinelAuth string,
	startupInitialInterval, startupMaxInterval, startupMaxElapsedTime time.Duration,
	closeConnectionOnReadOnlyError bool,
) Client {
	return newClientImpl(ctx, scope, useTls, auth, redisSocketType, redisType, url, poolSize,
		pipelineWindow, pipelineLimit, tlsConfig, healthCheckActiveConnection, srv,
		timeout, poolOnEmptyBehavior, sentinelAuth,
		startupInitialInterval, startupMaxInterval, startupMaxElapsedTime, 1,
		closeConnectionOnReadOnlyError, nil, clientOptions{ioTimeout: timeout})
}

// sentinelClientOptions returns the options for connections to Redis
// Sentinel nodes. They are not bounded by the I/O timeout: radix keeps a
// pubsub connection to the sentinels that waits for messages indefinitely
// and pings it on its own schedule. TCP options still apply.
func sentinelClientOptions(opts clientOptions) clientOptions {
	opts.ioTimeout = 0
	return opts
}

// clientOptions holds the settings that bound how long a Redis client waits
// on a connection that stopped answering.
type clientOptions struct {
	// ioTimeout bounds every read and write on a Redis connection. 0 disables it.
	ioTimeout time.Duration
	// maxInflight bounds the number of commands in flight; further commands
	// fail right away. 0 means unbounded.
	maxInflight int
	// tcpKeepAlive is the TCP keepalive idle time and probe interval. 0 uses
	// the default (10s), a negative value disables keepalive.
	tcpKeepAlive time.Duration
	// tcpUserTimeout sets TCP_USER_TIMEOUT (Linux only). 0 leaves the OS default.
	tcpUserTimeout time.Duration
	// healthCheckInterval enables a health probe that PINGs Redis at this
	// interval and fails healthComponent after healthCheckFailureThreshold
	// consecutive failures. 0 disables it.
	healthCheckInterval         time.Duration
	healthCheckFailureThreshold int
	healthComponent             string
}

func newClientImpl(ctx context.Context, scope stats.Scope, useTls bool, auth, redisSocketType, redisType, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int, tlsConfig *tls.Config, healthCheckActiveConnection bool, srv server.Server,
	timeout time.Duration, poolOnEmptyBehavior string, sentinelAuth string,
	startupInitialInterval, startupMaxInterval, startupMaxElapsedTime time.Duration,
	clusterPipelineParallelism int,
	closeConnectionOnReadOnlyError bool,
	credentialProvider CredentialProvider,
	opts clientOptions,
) *clientImpl {
	maskedUrl := utils.MaskCredentialsInUrl(url)
	logger.Warnf("connecting to redis on %s with pool size %d", maskedUrl, poolSize)

	// Create Dialer for connecting to Redis
	dialer := createDialer(timeout, opts, useTls, tlsConfig, auth, maskedUrl)
	// Pub/Sub can wait indefinitely between messages. Keep the same TLS/auth
	// and TCP configuration, but let its subscription loop manage I/O liveness.
	pubSubOpts := opts
	pubSubOpts.ioTimeout = 0
	pubSubDialer := dialer
	pubSubDialer.NetDialer = newNetDialer(timeout, pubSubOpts, useTls, tlsConfig)
	if opts.ioTimeout > 0 {
		logger.Warnf("Redis pool %s: reads and writes time out after %s", maskedUrl, opts.ioTimeout)
	} else {
		logger.Warnf("Redis pool %s: no I/O timeout; a connection to an unresponsive server blocks until the OS closes it", maskedUrl)
	}
	if opts.tcpUserTimeout > 0 {
		if tcpUserTimeoutSupported {
			logger.Warnf("Redis pool %s: TCP_USER_TIMEOUT set to %s", maskedUrl, opts.tcpUserTimeout)
		} else {
			logger.Warnf("Redis pool %s: TCP_USER_TIMEOUT is only supported on Linux, ignoring it", maskedUrl)
		}
	}

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

	if credentialProvider != nil {
		logger.Warnf("Redis pool %s: resolving credentials on every connection attempt", maskedUrl)
		poolConfig.Dialer = wrapDialerCredentialProvider(poolConfig.Dialer, credentialProvider)
		// Dedicated Pub/Sub connections must also refresh credentials on reconnect.
		pubSubDialer = wrapDialerCredentialProvider(pubSubDialer, credentialProvider)
	}

	// Wrap connections to correct which errors make the pool discard them
	// (see wrappedConn). Installed last because CustomConn replaces every
	// other Dialer field (TLS, auth, write buffering), which must all be final
	// before being captured.
	if closeConnectionOnReadOnlyError {
		// Discard pooled connections whose commands fail with READONLY (the
		// server behind them was demoted to replica by a failover) so the
		// pool re-dials and reaches the current master.
		logger.Warnf("Redis pool %s: closing connections on READONLY error replies", maskedUrl)
	}
	poolConfig.Dialer = wrapDialerConn(poolConfig.Dialer, closeConnectionOnReadOnlyError)

	effectivePipelineParallelism := clusterPipelineParallelism
	if isCluster {
		effectivePipelineParallelism = effectiveClusterPipelineParallelism(clusterPipelineParallelism, poolSize)
		switch {
		case clusterPipelineParallelism == 0:
			logger.Warnf("Redis cluster pipeline parallelism: auto bounded to Redis pool size (%d concurrent groups)", effectivePipelineParallelism)
		case clusterPipelineParallelism != effectivePipelineParallelism:
			logger.Warnf("Redis cluster pipeline parallelism: configured value %d exceeds Redis pool size %d; bounded to %d concurrent groups", clusterPipelineParallelism, poolSize, effectivePipelineParallelism)
		case effectivePipelineParallelism == 1:
			logger.Warnf("Redis cluster pipeline parallelism: disabled (serial legacy behavior)")
		default:
			logger.Warnf("Redis cluster pipeline parallelism: bounded to %d concurrent groups", effectivePipelineParallelism)
		}
	}

	// radix v4 pools have a fixed size, and pipelines share connections, so a
	// command only waits for a connection when the pool has none, i.e. when
	// Redis is unreachable and the pool is re-dialing. That wait is bounded by
	// the request's context (see clientImpl.do).
	//
	// - WAIT (default): wait for a connection, up to the request deadline.
	// - ERROR: fail right away when the pool has no connection.
	// - CREATE: not supported; radix v4 cannot create overflow connections.
	failWhenNoConnection := false
	switch strings.ToUpper(poolOnEmptyBehavior) {
	case "WAIT":
		logger.Warnf("Redis pool %s: on-empty=WAIT (commands wait for a connection up to their deadline)", maskedUrl)
	case "CREATE":
		panic(RedisError("REDIS_POOL_ON_EMPTY_BEHAVIOR=CREATE is not supported in radix v4: the pool has a fixed size and cannot create overflow connections. Remove this setting or set it to WAIT or ERROR, and consider increasing REDIS_POOL_SIZE."))
	case "ERROR":
		logger.Warnf("Redis pool %s: on-empty=ERROR (commands fail right away while the pool has no connection)", maskedUrl)
		failWhenNoConnection = true
	default:
		logger.Warnf("Redis pool %s: using v4 default (fixed size=%d, commands wait for a connection up to their deadline)", maskedUrl, poolSize)
	}

	poolFunc := func(ctx context.Context, network, addr string) (radix.Client, error) {
		return poolConfig.New(ctx, network, addr)
	}

	// Validate sentinel URL format early (before retry loop) since it's a configuration error.
	var sentinelMasterName string
	var sentinelAddrs []string
	if strings.ToLower(redisType) == "sentinel" {
		var parseErr error
		sentinelMasterName, sentinelAddrs, parseErr = parseSentinelUrl(url)
		if parseErr != nil {
			panic(RedisError(parseErr.Error()))
		}
	}

	b := &backoff.Backoff{
		Min:    startupInitialInterval,
		Max:    startupMaxInterval,
		Factor: 2,
		Jitter: true,
	}

	startTime := time.Now()

	retryOrDie := func(lastErr error) {
		elapsed := time.Since(startTime)
		if startupMaxElapsedTime > 0 && elapsed >= startupMaxElapsedTime {
			panic(RedisError(fmt.Sprintf("timed out waiting for Redis connection to %s after %s: %v", maskedUrl, elapsed.Round(time.Millisecond), lastErr)))
		}
		d := b.Duration()
		logger.Warnf("Retrying Redis connection to %s in %s (elapsed: %s): %v", maskedUrl, d, elapsed.Round(time.Millisecond), lastErr)
		select {
		case <-time.After(d):
		case <-ctx.Done():
			panic(RedisError(fmt.Sprintf("context cancelled while waiting for Redis connection to %s: %v", maskedUrl, ctx.Err())))
		}
	}

	var client redisClient
	for {
		var err error
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
			sentinelDialer := createDialer(timeout, sentinelClientOptions(opts), useTls, tlsConfig, sentinelAuth, fmt.Sprintf("sentinel(%s)", maskedUrl))
			sentinelConfig := radix.SentinelConfig{
				PoolConfig:     poolConfig,
				SentinelDialer: sentinelDialer,
			}
			client, err = sentinelConfig.New(ctx, sentinelMasterName, sentinelAddrs)
		default:
			panic(RedisError("Unrecognized redis type " + redisType))
		}

		if err != nil {
			retryOrDie(err)
			continue
		}

		var pingResponse string
		if pingErr := client.Do(ctx, radix.Cmd(&pingResponse, "PING")); pingErr != nil {
			_ = client.Close()
			retryOrDie(pingErr)
			continue
		}
		if pingResponse != "PONG" {
			_ = client.Close()
			retryOrDie(fmt.Errorf("unexpected PING response: %q", pingResponse))
			continue
		}

		// Successfully connected.
		break
	}

	if srv != nil {
		if err := srv.HealthChecker().Ok(server.RedisHealthComponentName); err != nil {
			logger.Errorf("Unable to update health status after Redis connection: %s", err)
		}
	}

	ret := &clientImpl{
		client:                     client,
		dialPubSub:                 newPubSubDialer(client, pubSubDialer, redisSocketType, url),
		stats:                      stats,
		isCluster:                  isCluster,
		clusterPipelineParallelism: effectivePipelineParallelism,
		failWhenNoConnection:       failWhenNoConnection,
		ioTimeout:                  opts.ioTimeout,
	}
	if opts.maxInflight > 0 {
		logger.Warnf("Redis pool %s: at most %d commands in flight", maskedUrl, opts.maxInflight)
		ret.inflight = make(chan struct{}, opts.maxInflight)
	}
	if opts.healthCheckInterval > 0 && srv != nil {
		component := opts.healthComponent
		if component == "" {
			component = server.RedisPingHealthComponentName
		}
		// PINGs time out like any other command; startHealthProbe caps it to
		// the interval.
		probeTimeout := opts.ioTimeout
		if probeTimeout <= 0 {
			probeTimeout = timeout
		}
		logger.Warnf("Redis pool %s: health probe every %s, unhealthy after %d consecutive failures", maskedUrl, opts.healthCheckInterval, opts.healthCheckFailureThreshold)
		ret.startHealthProbe(srv.HealthChecker(), component, opts.healthCheckInterval, probeTimeout, opts.healthCheckFailureThreshold)
	}
	return ret
}

// newPubSubDialer opens dedicated connections using the command client's current
// topology. The returned callback is used serially by one subscription loop.
func newPubSubDialer(client redisClient, dialer radix.Dialer, network, addr string) func(context.Context) (radix.Conn, error) {
	var next uint64
	return func(ctx context.Context) (radix.Conn, error) {
		if multi, ok := client.(radix.MultiClient); ok {
			clients, err := multi.Clients()
			if err != nil {
				return nil, err
			}
			if len(clients) == 0 {
				return nil, fmt.Errorf("no Redis primary available for local cache invalidation")
			}
			addrs := make([]string, 0, len(clients))
			for addr := range clients {
				addrs = append(addrs, addr)
			}
			// Stable rotation ensures a stalled cluster node cannot starve others.
			sort.Strings(addrs)
			addr = addrs[next%uint64(len(addrs))]
			next++
			network = "tcp"
		}
		return dialer.Dial(ctx, network, addr)
	}
}

func (c *clientImpl) DoCmd(rcv interface{}, cmd, key string, args ...interface{}) error {
	// Combine key and args into a single slice
	allArgs := make([]interface{}, 0, 1+len(args))
	allArgs = append(allArgs, key)
	allArgs = append(allArgs, args...)
	reply := newReply(rcv)
	action := radix.FlatCmd(reply, cmd, allArgs...)
	return c.do(context.Background(), func(ctx context.Context) error {
		return c.client.Do(ctx, action)
	}, func() { copyReply(rcv, reply) })
}

func (c *clientImpl) Close() error {
	if c.stopProbe != nil {
		c.stopOnce.Do(func() { close(c.stopProbe) })
		<-c.probeDone
	}
	return c.client.Close()
}

func (c *clientImpl) NumActiveConns() int {
	return int(c.stats.connectionActive.Value())
}

func (c *clientImpl) PipeAppend(pipeline Pipeline, rcv interface{}, cmd, key string, args ...interface{}) Pipeline {
	return c.PipeAppendWithRoutingKey(pipeline, key, rcv, cmd, key, args...)
}

func (c *clientImpl) PipeAppendWithRoutingKey(pipeline Pipeline, routingKey string, rcv interface{}, cmd, key string, args ...interface{}) Pipeline {
	// Combine key and args into a single slice
	allArgs := make([]interface{}, 0, 1+len(args))
	allArgs = append(allArgs, key)
	allArgs = append(allArgs, args...)
	reply := newReply(rcv)
	// The cluster client routes by ActionProperties.Keys, and radix's default
	// resolver classifies EVAL/EVALSHA as keyless.
	cfg := radix.CmdConfig{
		ActionProperties: func(cmd string, args ...string) radix.ActionProperties {
			props := radix.DefaultActionProperties(cmd, args...)
			props.Keys = []string{routingKey}
			return props
		},
	}
	return append(pipeline, PipelineAction{
		Action: cfg.FlatCmd(reply, cmd, allArgs...),
		Key:    routingKey,
		rcv:    rcv,
		reply:  reply,
	})
}

// PipeDo returns when the pipeline completed or ctx is done, whichever comes
// first. Receivers are only written when it returns no error.
func (c *clientImpl) PipeDo(ctx context.Context, pipeline Pipeline) error {
	return c.do(ctx, func(ctx context.Context) error {
		return c.pipeDo(ctx, pipeline)
	}, func() { commitReplies(pipeline) })
}

func (c *clientImpl) pipeDo(ctx context.Context, pipeline Pipeline) error {
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

// executeGroupedPipeline routes a pipeline of Redis actions in cluster mode
// via a three-tier dispatch:
//
//  1. Single-action fast path (len==1): skip grouping entirely.
//  2. Serial compatibility path: clusterPipelineParallelism == 1 preserves
//     the pre-parallelization behavior.
//  3. General path: group actions by key (same-key commands like INCRBY +
//     EXPIRE are still pipelined together) and execute groups concurrently
//     via errgroup with clusterPipelineParallelism as the max concurrent group
//     count.
func (c *clientImpl) executeGroupedPipeline(ctx context.Context, pipeline Pipeline) error {
	// Tier 1: single action — skip grouping, skip map alloc.
	if len(pipeline) == 1 {
		return c.client.Do(ctx, pipeline[0].Action)
	}

	// Tier 2: group by key, preserving first-occurrence order.
	groups := make([][]radix.Action, 0, len(pipeline))
	keyToIndex := make(map[string]int, len(pipeline))

	for _, pa := range pipeline {
		if idx, exists := keyToIndex[pa.Key]; exists {
			groups[idx] = append(groups[idx], pa.Action)
		} else {
			keyToIndex[pa.Key] = len(groups)
			groups = append(groups, []radix.Action{pa.Action})
		}
	}

	if c.clusterPipelineParallelism == 1 {
		return c.doPipelineGroupsSerial(ctx, groups)
	}

	// Execute groups concurrently.
	eg, egCtx := errgroup.WithContext(ctx)
	if c.clusterPipelineParallelism > 1 {
		eg.SetLimit(c.clusterPipelineParallelism)
	}
	for _, actions := range groups {
		actions := actions
		eg.Go(func() error {
			return c.doPipelineGroup(egCtx, actions)
		})
	}
	return eg.Wait()
}

func (c *clientImpl) doPipelineGroupsSerial(ctx context.Context, groups [][]radix.Action) error {
	for _, actions := range groups {
		if err := c.doPipelineGroup(ctx, actions); err != nil {
			return err
		}
	}
	return nil
}

func (c *clientImpl) doPipelineGroup(ctx context.Context, actions []radix.Action) error {
	if len(actions) == 1 {
		return c.client.Do(ctx, actions[0])
	}
	p := radix.NewPipeline()
	for _, action := range actions {
		p.Append(action)
	}
	return c.client.Do(ctx, p)
}
