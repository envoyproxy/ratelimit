package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	logger "github.com/sirupsen/logrus"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthChecker struct {
	sync.Mutex
	grpc      *health.Server
	healthMap map[string]bool
	// readinessMap holds the components that only affect readiness (see
	// ReadinessHandler), not the health check or the gRPC health service.
	readinessMap map[string]bool
	ok           uint32
	ready        uint32
	name         string
}

const (
	ConfigHealthComponentName = "config"
	RedisHealthComponentName  = "redis"
	// RedisPingHealthComponentName and RedisPerSecondPingHealthComponentName
	// are registered as readiness components when the Redis health probe is
	// enabled.
	RedisPingHealthComponentName          = "redis_ping"
	RedisPerSecondPingHealthComponentName = "redis_per_second_ping"
	SigtermComponentName                  = "sigterm"
)

func areAllComponentsHealthy(healthMap map[string]bool) bool {
	allComponentsHealthy := true
	for _, value := range healthMap {
		if !value {
			allComponentsHealthy = false
			break
		}
	}
	return allComponentsHealthy
}

// NewHealthChecker
// Only set the overall health to be Ok if all individual components are healthy.
func NewHealthChecker(grpcHealthServer *health.Server, name string, healthyWithAtLeastOneConfigLoad bool) *HealthChecker {
	ret := &HealthChecker{}
	ret.name = name

	ret.healthMap = make(map[string]bool)
	ret.readinessMap = make(map[string]bool)
	// Store health states of components into map
	// Redis starts unhealthy; it is marked healthy once the connection is confirmed at startup.
	ret.healthMap[RedisHealthComponentName] = false
	if healthyWithAtLeastOneConfigLoad {
		// config starts in failed state since we need at least one config loaded to be healthy
		ret.healthMap[ConfigHealthComponentName] = false
	}
	// True indicates we have not received sigterm
	ret.healthMap[SigtermComponentName] = true

	ret.grpc = grpcHealthServer

	if areAllComponentsHealthy(ret.healthMap) {
		ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_SERVING)
		ret.ok = 1
		ret.ready = 1
	} else {
		ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_NOT_SERVING)
		ret.ok = 0
	}

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	go func() {
		<-sigterm
		_ = ret.Fail(SigtermComponentName)
	}()

	return ret
}

func (hc *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ok := atomic.LoadUint32(&hc.ok)
	if ok == 1 {
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(500)
	}
}

// ReadinessHandler serves readiness: healthy when every component the health
// check covers is healthy and so is every readiness component. Readiness
// components report conditions a restart cannot fix, such as Redis not
// answering, so they are kept out of the health check, which is commonly used
// for liveness.
func (hc *HealthChecker) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadUint32(&hc.ready) == 1 {
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(500)
		}
	})
}

// updateReadiness recomputes readiness. Must be called with hc locked.
func (hc *HealthChecker) updateReadiness() {
	ready := uint32(0)
	if areAllComponentsHealthy(hc.healthMap) && areAllComponentsHealthy(hc.readinessMap) {
		ready = 1
	}
	atomic.StoreUint32(&hc.ready, ready)
}

func (hc *HealthChecker) Fail(componentName string) error {
	hc.Lock()
	defer hc.Unlock()
	if _, ok := hc.healthMap[componentName]; ok {
		// Set component to be unhealthy
		hc.healthMap[componentName] = false
		atomic.StoreUint32(&hc.ok, 0)
		atomic.StoreUint32(&hc.ready, 0)
		hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_NOT_SERVING)
	} else if _, ok := hc.readinessMap[componentName]; ok {
		hc.readinessMap[componentName] = false
		atomic.StoreUint32(&hc.ready, 0)
	} else {
		errorText := fmt.Sprintf("Invalid component: %s", componentName)
		logger.Error(errorText)
		return errors.New(errorText)
	}
	return nil
}

func (hc *HealthChecker) Ok(componentName string) error {
	hc.Lock()
	defer hc.Unlock()

	if _, ok := hc.healthMap[componentName]; ok {
		// Set component to be healthy
		hc.healthMap[componentName] = true
		allComponentsHealthy := areAllComponentsHealthy(hc.healthMap)
		logger.Debugf("Health status of components: %v, all healthy: %t", hc.healthMap, allComponentsHealthy)
		if allComponentsHealthy {
			atomic.StoreUint32(&hc.ok, 1)
			hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_SERVING)
		}
		hc.updateReadiness()
	} else if _, ok := hc.readinessMap[componentName]; ok {
		hc.readinessMap[componentName] = true
		hc.updateReadiness()
	} else {
		errorText := fmt.Sprintf("Invalid component: %s", componentName)
		logger.Error(errorText)
		return errors.New(errorText)
	}

	return nil
}

// AddReadinessComponent registers a component that only affects readiness
// (see ReadinessHandler). Registering an existing component only sets its
// health.
func (hc *HealthChecker) AddReadinessComponent(componentName string, healthy bool) {
	hc.Lock()
	defer hc.Unlock()

	hc.readinessMap[componentName] = healthy
	hc.updateReadiness()
}

func (hc *HealthChecker) Server() *health.Server {
	return hc.grpc
}
