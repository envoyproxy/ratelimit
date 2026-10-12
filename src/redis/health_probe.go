package redis

import (
	"context"
	"time"

	"github.com/mediocregopher/radix/v4"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/server"
)

// healthProbe periodically PINGs Redis and marks a health component unhealthy
// after failureThreshold consecutive failures, and healthy again after one
// success.
//
// Health otherwise only tracks whether the pool has connections at all
// (REDIS_HEALTH_CHECK_ACTIVE_CONNECTION), which stays true while every
// connection is stuck waiting on a server that stopped answering. The probe
// goes through the same client path as rate limit calls, so it fails whenever
// they would.
type healthProbe struct {
	client           *clientImpl
	health           *server.HealthChecker
	component        string
	interval         time.Duration
	timeout          time.Duration
	failureThreshold int
	failures         int
}

func (c *clientImpl) startHealthProbe(health *server.HealthChecker, component string, interval, timeout time.Duration, failureThreshold int) {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if timeout <= 0 || timeout > interval {
		timeout = interval
	}
	p := &healthProbe{
		client:           c,
		health:           health,
		component:        component,
		interval:         interval,
		timeout:          timeout,
		failureThreshold: failureThreshold,
	}
	// The client connected at startup, so start healthy.
	health.AddComponent(component, true)

	c.stopProbe = make(chan struct{})
	c.probeDone = make(chan struct{})
	go p.run(c.stopProbe, c.probeDone)
}

func (p *healthProbe) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.probe()
		}
	}
}

func (p *healthProbe) probe() {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	var pong string
	err := p.client.do(ctx, func(ctx context.Context) error {
		return p.client.client.Do(ctx, radix.Cmd(&pong, "PING"))
	}, nil)
	if err == nil && pong != "PONG" {
		err = RedisError("unexpected PING reply: " + pong)
	}

	if err == nil {
		if p.failures >= p.failureThreshold {
			logger.Warnf("Redis health probe %s: PING succeeded, marking healthy", p.component)
		}
		p.failures = 0
		if err := p.health.Ok(p.component); err != nil {
			logger.Errorf("Unable to update health status: %s", err)
		}
		return
	}

	p.failures++
	logger.Warnf("Redis health probe %s: PING failed (%d consecutive): %v", p.component, p.failures, err)
	if p.failures == p.failureThreshold {
		logger.Errorf("Redis health probe %s: %d consecutive PING failures, marking unhealthy", p.component, p.failures)
		if err := p.health.Fail(p.component); err != nil {
			logger.Errorf("Unable to update health status: %s", err)
		}
	}
}
