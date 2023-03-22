package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/signal"
	"syscall"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ratelimit/src/server"
)

func TestHealthCheck(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	recorder := httptest.NewRecorder()

	hc := server.NewHealthChecker(health.NewServer(), "ratelimit", false)

	r, _ := http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 200 != recorder.Code {
		t.Errorf("expected code 200 actual %d", recorder.Code)
	}

	if "OK" != recorder.Body.String() {
		t.Errorf("expected body 'OK', got '%s'", recorder.Body.String())
	}

	err := hc.Fail(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 500 != recorder.Code {
		t.Errorf("expected code 500 actual %d", recorder.Code)
	}

	err = hc.Ok(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 200 != recorder.Code {
		t.Errorf("expected code 200 actual %d", recorder.Code)
	}

	if "OK" != recorder.Body.String() {
		t.Errorf("expected body 'OK', got '%s'", recorder.Body.String())
	}
}

func TestHealthyWithAtLeastOneConfigLoaded(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	recorder := httptest.NewRecorder()

	hc := server.NewHealthChecker(health.NewServer(), "ratelimit", true)

	r, _ := http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 500 != recorder.Code {
		t.Errorf("expected code 500 actual %d", recorder.Code)
	}

	err := hc.Ok(server.ConfigHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating config health status")
	}

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 200 != recorder.Code {
		t.Errorf("expected code 200 actual %d", recorder.Code)
	}

	if "OK" != recorder.Body.String() {
		t.Errorf("expected body 'OK', got '%s'", recorder.Body.String())
	}

	err = hc.Fail(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 500 != recorder.Code {
		t.Errorf("expected code 500 actual %d", recorder.Code)
	}

	err = hc.Ok(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 200 != recorder.Code {
		t.Errorf("expected code 200 actual %d", recorder.Code)
	}

	if "OK" != recorder.Body.String() {
		t.Errorf("expected body 'OK', got '%s'", recorder.Body.String())
	}
}

func TestGrpcHealthCheck(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	grpcHealthServer := health.NewServer()
	hc := server.NewHealthChecker(grpcHealthServer, "ratelimit", false)
	healthpb.RegisterHealthServer(grpc.NewServer(), grpcHealthServer)

	req := &healthpb.HealthCheckRequest{
		Service: "ratelimit",
	}

	res, _ := grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_SERVING != res.Status {
		t.Errorf("expected status SERVING actual %v", res.Status)
	}

	err := hc.Ok(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_SERVING != res.Status {
		t.Errorf("expected status SERVING actual %v", res.Status)
	}

	err = hc.Fail(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_NOT_SERVING != res.Status {
		t.Errorf("expected status NOT_SERVING actual %v", res.Status)
	}
}

func TestGrpcHealthyWithAtLeastOneConfigLoaded(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	grpcHealthServer := health.NewServer()
	hc := server.NewHealthChecker(grpcHealthServer, "ratelimit", true)
	healthpb.RegisterHealthServer(grpc.NewServer(), grpcHealthServer)

	req := &healthpb.HealthCheckRequest{
		Service: "ratelimit",
	}

	res, _ := grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_NOT_SERVING != res.Status {
		t.Errorf("expected status NOT_SERVING actual %v", res.Status)
	}

	err := hc.Ok(server.ConfigHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating config health status")
	}
	err = hc.Ok(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_SERVING != res.Status {
		t.Errorf("expected status SERVING actual %v", res.Status)
	}

	err = hc.Fail(server.ConfigHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating config health status")
	}
	err = hc.Fail(server.RedisHealthComponentName)
	if err != nil {
		t.Errorf("Expected no errors for updating redis health status")
	}

	res, _ = grpcHealthServer.Check(context.Background(), req)
	if healthpb.HealthCheckResponse_NOT_SERVING != res.Status {
		t.Errorf("expected status NOT_SERVING actual %v", res.Status)
	}
}
