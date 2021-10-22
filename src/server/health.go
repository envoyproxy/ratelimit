package server

import (
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type HealthChecker struct {
	sync.Mutex
	grpc     *health.Server
	ok       uint32
	name     string
	stopping bool
}

// NewHealthChecker creates health checker in NOT_SERVING mode and awaits Pass() after successful config load
func NewHealthChecker(grpcHealthServer *health.Server, name string) *HealthChecker {
	ret := &HealthChecker{}
	ret.ok = 0
	ret.name = name

	ret.grpc = grpcHealthServer
	ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_NOT_SERVING)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	go func() {
		<-sigterm
		ret.Lock()
		defer ret.Unlock()
		ret.stopping = true
		atomic.StoreUint32(&ret.ok, 0)
		ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_NOT_SERVING)
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

func (hc *HealthChecker) Fail() {
	hc.Lock()
	defer hc.Unlock()
	atomic.StoreUint32(&hc.ok, 0)
	hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_NOT_SERVING)
}

func (hc *HealthChecker) Pass() {
	hc.Lock()
	defer hc.Unlock()
	if hc.stopping {
		return
	}

	atomic.StoreUint32(&hc.ok, 1)
	hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_SERVING)
}

func (hc *HealthChecker) Server() *health.Server {
	return hc.grpc
}
