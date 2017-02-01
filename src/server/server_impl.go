package server

import (
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"sort"

	logger "github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/lyft/goruntime/loader"
	"github.com/lyft/gostats"
	"github.com/lyft/ratelimit/src/settings"
	"google.golang.org/grpc"
)

type serverDebugListener struct {
	endpoints map[string]string
	debugMux  *http.ServeMux
}

type server struct {
	port          int
	grpcPort      int
	debugPort     int
	router        *mux.Router
	grpcServer    *grpc.Server
	store         stats.Store
	scope         stats.Scope
	runtime       loader.IFace
	debugListener serverDebugListener
}

func (server *server) AddDebugHttpEndpoint(path string, help string, handler http.HandlerFunc) {
	server.debugListener.debugMux.HandleFunc(path, handler)
	server.debugListener.endpoints[path] = help
}

func (server *server) GrpcServer() *grpc.Server {
	return server.grpcServer
}

func (server *server) Start() {
	go func() {
		addr := fmt.Sprintf(":%d", server.debugPort)
		logger.Warnf("Listening for debug on '%s'", addr)
		logger.Info(http.ListenAndServe(addr, server.debugListener.debugMux))
	}()

	go server.startGrpc()

	addr := fmt.Sprintf(":%d", server.port)
	logger.Warnf("Listening for HTTP on '%s'", addr)
	logger.Fatal(http.ListenAndServe(addr, server.router))
}

func (server *server) startGrpc() {
	addr := fmt.Sprintf(":%d", server.grpcPort)
	logger.Warnf("Listening for gRPC on '%s'", addr)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatalf("failed to listen: %v", err)
	}
	server.grpcServer.Serve(lis)
}

func (server *server) Scope() stats.Scope {
	return server.scope
}

func (server *server) Runtime() loader.IFace {
	return server.runtime
}

func NewServer(name string, opts ...settings.Option) Server {
	return newServer(name, opts...)
}

func newServer(name string, opts ...settings.Option) *server {
	s := settings.NewSettings()

	for _, opt := range opts {
		opt(&s)
	}

	ret := new(server)
	ret.grpcServer = grpc.NewServer(s.GrpcUnaryInterceptor)

	// setup ports
	ret.port = s.Port
	ret.grpcPort = s.GrpcPort
	ret.debugPort = s.DebugPort

	// setup stats
	ret.store = stats.NewDefaultStore()
	ret.scope = ret.store.Scope(name)
	ret.store.AddStatGenerator(stats.NewRuntimeStats(ret.scope.Scope("go")))

	// setup runtime
	ret.runtime = loader.New(s.RuntimePath, s.RuntimeSubdirectory, ret.store.Scope("runtime"))

	// setup http router
	ret.router = mux.NewRouter()

	// setup healthcheck path
	ret.router.Path("/healthcheck").Handler(NewHealthChecker())

	// setup default debug listener
	ret.debugListener.debugMux = http.NewServeMux()
	ret.debugListener.endpoints = map[string]string{}
	ret.AddDebugHttpEndpoint(
		"/debug/pprof/",
		"root of various pprof endpoints. hit for help.",
		func(writer http.ResponseWriter, request *http.Request) {
			pprof.Index(writer, request)
		})

	// setup stats endpoint
	ret.AddDebugHttpEndpoint(
		"/stats",
		"print out stats",
		func(writer http.ResponseWriter, request *http.Request) {
			expvar.Do(func(kv expvar.KeyValue) {
				io.WriteString(writer, fmt.Sprintf("%s: %s\n", kv.Key, kv.Value))
			})
		})

	// setup debug root
	ret.debugListener.debugMux.HandleFunc(
		"/",
		func(writer http.ResponseWriter, request *http.Request) {
			sortedKeys := []string{}
			for key := range ret.debugListener.endpoints {
				sortedKeys = append(sortedKeys, key)
			}

			sort.Strings(sortedKeys)
			for _, key := range sortedKeys {
				io.WriteString(
					writer, fmt.Sprintf("%s: %s\n", key, ret.debugListener.endpoints[key]))
			}
		})

	return ret
}
