package server

import (
	"context"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/envoyproxy/ratelimit/src/provider"
	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/gorilla/mux"
	reuseport "github.com/kavu/go_reuseport"
	"github.com/lyft/goruntime/loader"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/settings"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("ratelimit server")

type serverDebugListener struct {
	endpoints map[string]string
	debugMux  *http.ServeMux
	listener  net.Listener
}

type grpcListenType int

const (
	tcp              grpcListenType = 0
	unixDomainSocket grpcListenType = 1
)

type server struct {
	httpAddress      string
	grpcAddress      string
	grpcListenType   grpcListenType
	debugAddress     string
	router           *mux.Router
	grpcServer       *grpc.Server
	store            gostats.Store
	scope            gostats.Scope
	provider         provider.RateLimitConfigProvider
	runtime          loader.IFace
	debugListener    serverDebugListener
	httpServer       *http.Server
	listenerMu       sync.Mutex
	health           *HealthChecker
	grpcCertProvider *provider.CertProvider
}

func (server *server) AddDebugHttpEndpoint(path string, help string, handler http.HandlerFunc) {
	server.listenerMu.Lock()
	defer server.listenerMu.Unlock()
	server.debugListener.debugMux.HandleFunc(path, handler)
	server.debugListener.endpoints[path] = help
}

// create an http/1 handler at the /json endpoint which allows this ratelimit service to work with
// clients that cannot use the gRPC interface (e.g. lua)
// example usage from cURL with domain "dummy" and descriptor "perday":
// echo '{"domain": "dummy", "descriptors": [{"entries": [{"key": "perday"}]}]}' | curl -vvvXPOST --data @/dev/stdin localhost:8080/json
func NewJsonHandler(svc pb.RateLimitServiceServer) func(http.ResponseWriter, *http.Request) {
	return func(writer http.ResponseWriter, request *http.Request) {
		var req pb.RateLimitRequest

		ctx := context.Background()

		body, err := io.ReadAll(request.Body)
		if err != nil {
			logger.Warnf("error: %s", err.Error())
			writeHttpStatus(writer, http.StatusBadRequest)
			return
		}

		if err := protojson.Unmarshal(body, &req); err != nil {
			logger.Warnf("error: %s", err.Error())
			writeHttpStatus(writer, http.StatusBadRequest)
			return
		}

		resp, err := svc.ShouldRateLimit(ctx, &req)
		if err != nil {
			logger.Warnf("error: %s", err.Error())
			writeHttpStatus(writer, http.StatusBadRequest)
			return
		}

		// Generate trace
		_, span := tracer.Start(ctx, "NewJsonHandler Remaining Execution",
			trace.WithAttributes(
				attribute.String("response", resp.String()),
			),
		)
		defer span.End()

		logger.Debugf("resp:%s", resp)
		if resp == nil {
			logger.Error("nil response")
			writeHttpStatus(writer, http.StatusInternalServerError)
			return
		}

		jsonResp, err := protojson.Marshal(resp)
		if err != nil {
			logger.Errorf("error marshaling proto3 to json: %s", err.Error())
			writeHttpStatus(writer, http.StatusInternalServerError)
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		if resp == nil || resp.OverallCode == pb.RateLimitResponse_UNKNOWN {
			writer.WriteHeader(http.StatusInternalServerError)
		} else if resp.OverallCode == pb.RateLimitResponse_OVER_LIMIT {
			writer.WriteHeader(http.StatusTooManyRequests)
		}
		writer.Write(jsonResp)
	}
}

func writeHttpStatus(writer http.ResponseWriter, code int) {
	http.Error(writer, http.StatusText(code), code)
}

func getProviderImpl(s settings.Settings, statsManager stats.Manager, rootStore gostats.Store) provider.RateLimitConfigProvider {
	switch s.ConfigType {
	case "FILE":
		return provider.NewFileProvider(s, statsManager, rootStore)
	case "GRPC_XDS_SOTW":
		return provider.NewXdsGrpcSotwProvider(s, statsManager)
	default:
		logger.Fatalf("Invalid setting for ConfigType: %s", s.ConfigType)
		panic("This line should not be reachable")
	}
}

func (server *server) AddJsonHandler(svc pb.RateLimitServiceServer) {
	server.router.HandleFunc("/json", NewJsonHandler(svc))
}

func (server *server) GrpcServer() *grpc.Server {
	return server.grpcServer
}

func (server *server) Start() {
	go func() {
		logger.Warnf("Listening for debug on '%s'", server.debugAddress)
		var err error
		server.listenerMu.Lock()
		server.debugListener.listener, err = reuseport.Listen("tcp", server.debugAddress)
		server.listenerMu.Unlock()

		if err != nil {
			logger.Errorf("Failed to open debug HTTP listener: '%+v'", err)
			return
		}
		err = http.Serve(server.debugListener.listener, server.debugListener.debugMux)
		logger.Infof("Failed to start debug server '%+v'", err)
	}()

	go server.startGrpc()

	server.handleGracefulShutdown()

	logger.Warnf("Listening for HTTP on '%s'", server.httpAddress)
	list, err := reuseport.Listen("tcp", server.httpAddress)
	if err != nil {
		logger.Fatalf("Failed to open HTTP listener: '%+v'", err)
	}
	srv := &http.Server{Handler: server.router}
	server.listenerMu.Lock()
	server.httpServer = srv
	server.listenerMu.Unlock()
	err = srv.Serve(list)

	if err != http.ErrServerClosed {
		logger.Fatal(err)
	}
}

func (server *server) startGrpc() {
	logger.Warnf("Listening for gRPC on '%s'", server.grpcAddress)
	var lis net.Listener
	var err error

	switch server.grpcListenType {
	case tcp:
		lis, err = reuseport.Listen("tcp", server.grpcAddress)
	case unixDomainSocket:
		lis, err = net.Listen("unix", server.grpcAddress)
	default:
		logger.Fatalf("Invalid gRPC listen type %v", server.grpcListenType)
	}

	if err != nil {
		logger.Fatalf("Failed to listen for gRPC on '%s': %v", server.grpcAddress, err)
	}
	server.grpcServer.Serve(lis)
}

func (server *server) Scope() gostats.Scope {
	return server.scope
}

func (server *server) Provider() provider.RateLimitConfigProvider {
	return server.provider
}

func NewServer(s settings.Settings, name string, statsManager stats.Manager, localCache *freecache.Cache, opts ...settings.Option) Server {
	return newServer(s, name, statsManager, localCache, opts...)
}

func newServer(s settings.Settings, name string, statsManager stats.Manager, localCache *freecache.Cache, opts ...settings.Option) *server {
	for _, opt := range opts {
		opt(&s)
	}

	ret := new(server)

	// setup stats
	ret.store = statsManager.GetStatsStore()
	ret.scope = ret.store.ScopeWithTags(name, s.ExtraTags)
	ret.store.AddStatGenerator(gostats.NewRuntimeStats(ret.scope.Scope("go")))
	if localCache != nil {
		ret.store.AddStatGenerator(limiter.NewLocalCacheStats(localCache, ret.scope.Scope("localcache")))
	}

	keepaliveOpt := grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionAge:      s.GrpcMaxConnectionAge,
		MaxConnectionAgeGrace: s.GrpcMaxConnectionAgeGrace,
	})
	grpcOptions := []grpc.ServerOption{
		keepaliveOpt,
		grpc.ChainUnaryInterceptor(
			s.GrpcUnaryInterceptor, // chain otel interceptor after the input interceptor
			otelgrpc.UnaryServerInterceptor(),
		),
		grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
	}
	if s.GrpcServerUseTLS {
		grpcServerTlsConfig := s.GrpcServerTlsConfig
		ret.grpcCertProvider = provider.NewCertProvider(s, ret.store, s.GrpcServerTlsCert, s.GrpcServerTlsKey)
		// Remove the static certificates and use the provider via the GetCertificate function
		grpcServerTlsConfig.Certificates = nil
		grpcServerTlsConfig.GetCertificate = ret.grpcCertProvider.GetCertificateFunc()
		// Verify client SAN if provided
		if s.GrpcClientTlsSAN != "" {
			grpcServerTlsConfig.VerifyPeerCertificate = verifyClient(grpcServerTlsConfig.ClientCAs, s.GrpcClientTlsSAN)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(grpcServerTlsConfig)))
	}
	ret.grpcServer = grpc.NewServer(grpcOptions...)

	// setup listen addresses
	ret.httpAddress = net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	if s.GrpcUds != "" {
		ret.grpcAddress = s.GrpcUds
		ret.grpcListenType = unixDomainSocket
	} else {
		ret.grpcAddress = net.JoinHostPort(s.GrpcHost, strconv.Itoa(s.GrpcPort))
		ret.grpcListenType = tcp
	}
	ret.debugAddress = net.JoinHostPort(s.DebugHost, strconv.Itoa(s.DebugPort))

	// setup config provider
	ret.provider = getProviderImpl(s, statsManager, ret.store)

	// setup http router
	ret.router = mux.NewRouter()

	// setup healthcheck path
	ret.health = NewHealthChecker(health.NewServer(), "ratelimit", s.HealthyWithAtLeastOneConfigLoaded)
	ret.router.Path("/healthcheck").Handler(ret.health)
	healthpb.RegisterHealthServer(ret.grpcServer, ret.health.Server())

	// setup default debug listener
	ret.debugListener.debugMux = http.NewServeMux()
	ret.debugListener.endpoints = map[string]string{}
	ret.AddDebugHttpEndpoint(
		"/debug/pprof/",
		"root of various pprof endpoints. hit for help.",
		func(writer http.ResponseWriter, request *http.Request) {
			pprof.Index(writer, request)
		})

	// setup cpu profiling endpoint
	ret.AddDebugHttpEndpoint(
		"/debug/pprof/profile",
		"CPU profiling endpoint",
		func(writer http.ResponseWriter, request *http.Request) {
			pprof.Profile(writer, request)
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

	// setup trace endpoint
	ret.AddDebugHttpEndpoint(
		"/debug/pprof/trace",
		"trace endpoint",
		func(writer http.ResponseWriter, request *http.Request) {
			pprof.Trace(writer, request)
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

func (server *server) Stop() {
	server.grpcServer.GracefulStop()
	server.listenerMu.Lock()
	defer server.listenerMu.Unlock()
	if server.debugListener.listener != nil {
		server.debugListener.listener.Close()
	}
	if server.httpServer != nil {
		server.httpServer.Close()
	}
	server.provider.Stop()
}

func (server *server) handleGracefulShutdown() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigs

		logger.Infof("Ratelimit server received %v, shutting down gracefully", sig)
		server.Stop()
		os.Exit(0)
	}()
}

func (server *server) HealthChecker() *HealthChecker {
	return server.health
}
