package factory

import (
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/storage/service"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/envoyproxy/ratelimit/src/storage/utils"
	"github.com/mediocregopher/radix/v3"
)

func NewRedis(scope stats.Scope, useTls bool, auth string, redisType string, url string, poolSize int,
	pipelineWindow time.Duration, pipelineLimit int) strategy.StorageStrategy {
	client := newRedisClient(scope, useTls, auth, redisType, url, poolSize, pipelineWindow, pipelineLimit)
	return strategy.RedisStrategy{
		Client: client,
	}
}

func newRedisClient(scope stats.Scope, useTls bool, auth string, redisType string, url string, poolSize int, pipelineWindow time.Duration, pipelineLimit int) service.RedisClientInterface {
	logger.Warnf("connecting to redis on %s with pool size %d", url, poolSize)

	df := func(network, addr string) (radix.Conn, error) {
		var dialOpts []radix.DialOpt

		if useTls {
			dialOpts = append(dialOpts, radix.DialUseTLS(&tls.Config{}))
		}

		if auth != "" {
			logger.Warnf("enabling authentication to redis on %s", url)

			dialOpts = append(dialOpts, radix.DialAuthPass(auth))
		}

		return radix.Dial(network, addr, dialOpts...)
	}

	stats := service.NewRedisStats(scope)
	opts := []radix.PoolOpt{radix.PoolConnFunc(df), radix.PoolWithTrace(service.PoolTrace(&stats))}

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
		client, err = poolFunc("tcp", url)
	case "cluster":
		urls := strings.Split(url, ",")
		if implicitPipelining == false {
			panic(utils.RedisError("Implicit Pipelining must be enabled to work with Redis Cluster Mode. Set values for REDIS_PIPELINE_WINDOW or REDIS_PIPELINE_LIMIT to enable implicit pipelining"))
		}
		logger.Warnf("Creating cluster with urls %v", urls)
		client, err = radix.NewCluster(urls, radix.ClusterPoolFunc(poolFunc))
	case "sentinel":
		urls := strings.Split(url, ",")
		if len(urls) < 2 {
			panic(utils.RedisError("Expected master name and a list of urls for the sentinels, in the format: <redis master name>,<sentinel1>,...,<sentineln>"))
		}
		client, err = radix.NewSentinel(urls[0], urls[1:], radix.SentinelPoolFunc(poolFunc))
	default:
		panic(utils.RedisError("Unrecognized redis type " + redisType))
	}

	utils.CheckError(err)

	// Check if connection is good
	var pingResponse string
	utils.CheckError(client.Do(radix.Cmd(&pingResponse, "PING")))
	if pingResponse != "PONG" {
		utils.CheckError(fmt.Errorf("connecting redis error: %s", pingResponse))
	}

	return &service.RedisClient{
		Client:             client,
		Stats:              stats,
		ImplicitPipelining: implicitPipelining,
	}
}
