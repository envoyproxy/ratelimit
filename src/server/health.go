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
	ok        uint32
	name      string
}

const (
	ConfigHealthComponentName = "config"
	RedisHealthComponentName  = "redis"
	SigtermComponentName      = "sigterm"
)

func areAllComponentsHealthy(healthMap map[string]bool) bool {
	allComponentsHealthy := true
	for _, value := range healthMap {
		if value == false {
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
	// Store health states of components into map
	ret.healthMap[RedisHealthComponentName] = true
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

func (hc *HealthChecker) Fail(componentName string) error {
	hc.Lock()
	defer hc.Unlock()
	if _, ok := hc.healthMap[componentName]; ok {
		// Set component to be unhealthy
		hc.healthMap[componentName] = false
		atomic.StoreUint32(&hc.ok, 0)
		hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_NOT_SERVING)
	} else {
		errorText := fmt.Sprintf("Invalid component: %s", componentName)
		logger.Errorf(errorText)
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

		if allComponentsHealthy {
			atomic.StoreUint32(&hc.ok, 1)
			hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_SERVING)
		}
	} else {
		errorText := fmt.Sprintf("Invalid component: %s", componentName)
		logger.Errorf(errorText)
		return errors.New(errorText)
	}

	return nil
}

func (hc *HealthChecker) Server() *health.Server {
	return hc.grpc
}
