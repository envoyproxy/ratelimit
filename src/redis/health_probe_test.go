package redis

import (
	"net/http"
	"net/http/httptest"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/health"

	"github.com/envoyproxy/ratelimit/src/server"
)

func healthStatus(hc *server.HealthChecker) int {
	rec := httptest.NewRecorder()
	hc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))
	return rec.Code
}

func TestHealthProbeTracksUnresponsiveRedis(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)
	hc := server.NewHealthChecker(health.NewServer(), "ratelimit", false)
	assert.NoError(t, hc.Ok(server.RedisHealthComponentName))

	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 2, false, nil, clientOptions{ioTimeout: time.Second})
	c.startHealthProbe(hc, server.RedisPingHealthComponentName, 20*time.Millisecond, 15*time.Millisecond, 3)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, http.StatusOK, healthStatus(hc))

	// Redis stops answering: connections stay open, so only the probe notices.
	srv.setBlackhole(true)
	assert.Eventually(t, func() bool { return healthStatus(hc) == http.StatusInternalServerError },
		2*time.Second, 10*time.Millisecond)

	srv.setBlackhole(false)
	assert.Eventually(t, func() bool { return healthStatus(hc) == http.StatusOK },
		5*time.Second, 10*time.Millisecond)
}

func TestHealthProbeToleratesIsolatedFailures(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)
	hc := server.NewHealthChecker(health.NewServer(), "ratelimit", false)
	assert.NoError(t, hc.Ok(server.RedisHealthComponentName))

	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{ioTimeout: time.Second})
	p := &healthProbe{client: c, health: hc, component: server.RedisPingHealthComponentName, timeout: 20 * time.Millisecond, failureThreshold: 3}
	hc.AddComponent(p.component, true)

	srv.setDelay(50 * time.Millisecond)
	p.probe()
	p.probe()
	assert.Equal(t, http.StatusOK, healthStatus(hc), "below the threshold")

	srv.setDelay(0)
	time.Sleep(100 * time.Millisecond) // let the slow replies drain
	p.probe()
	assert.Equal(t, 0, p.failures, "a success resets the count")

	srv.setDelay(50 * time.Millisecond)
	p.probe()
	p.probe()
	p.probe()
	assert.Equal(t, http.StatusInternalServerError, healthStatus(hc))
}

func TestCloseStopsHealthProbe(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)
	hc := server.NewHealthChecker(health.NewServer(), "ratelimit", false)

	srv := newFakeRedis(t, nil)
	c, _ := newFakeRedisClient(t, srv.addr(), 1, false, nil, clientOptions{ioTimeout: time.Second})
	c.startHealthProbe(hc, server.RedisPingHealthComponentName, 5*time.Millisecond, 5*time.Millisecond, 1)

	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not stop the health probe")
	}
}
