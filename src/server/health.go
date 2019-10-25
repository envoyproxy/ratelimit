package server

import (
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type healthChecker struct {
	grpc *health.Server
	ok   uint32
	name string
}

func NewHealthChecker(grpcHealthServer *health.Server, name string) *healthChecker {
	ret := &healthChecker{}
	ret.ok = 1
	ret.name = name

	ret.grpc = grpcHealthServer
	ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_SERVING)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	go func() {
		<-sigterm
		atomic.StoreUint32(&ret.ok, 0)
		ret.grpc.SetServingStatus(ret.name, healthpb.HealthCheckResponse_NOT_SERVING)
	}()

	return ret
}

func (hc *healthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ok := atomic.LoadUint32(&hc.ok)
	if ok == 1 {
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(500)
	}
}

func (hc *healthChecker) Fail() {
	atomic.StoreUint32(&hc.ok, 0)
	hc.grpc.SetServingStatus(hc.name, healthpb.HealthCheckResponse_NOT_SERVING)
}
