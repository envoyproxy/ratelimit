package server_test

import (
	"net/http"
	"net/http/httptest"
	"os/signal"
	"syscall"
	"testing"

	"github.com/lyft/ratelimit/src/server"
)

func TestHealthCheck(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	recorder := httptest.NewRecorder()

	hc := server.NewHealthChecker()

	r, _ := http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 200 != recorder.Code {
		t.Errorf("expected code 200 actual %d", recorder.Code)
	}

	if "OK" != recorder.Body.String() {
		t.Errorf("expected body 'OK', got '%s'", recorder.Body.String())
	}

	hc.Fail()

	recorder = httptest.NewRecorder()

	r, _ = http.NewRequest("GET", "http://1.2.3.4/healthcheck", nil)
	hc.ServeHTTP(recorder, r)

	if 500 != recorder.Code {
		t.Errorf("expected code 500 actual %d", recorder.Code)
	}

}
