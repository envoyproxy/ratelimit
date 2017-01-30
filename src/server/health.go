package server

import (
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

type healthChecker struct {
	ok uint32
}

func NewHealthChecker() *healthChecker {
	ret := &healthChecker{}
	ret.ok = 1

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)

	go func() {
		<-sigterm
		atomic.StoreUint32(&ret.ok, 0)
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
}
