package runner

import (
	"io"
	"math/rand"
	"net/http"
	"time"

	pb "github.com/lyft/ratelimit/proto/ratelimit"
	"github.com/lyft/ratelimit/src/config"
	"github.com/lyft/ratelimit/src/redis"
	"github.com/lyft/ratelimit/src/server"
	"github.com/lyft/ratelimit/src/service"
	"github.com/lyft/ratelimit/src/settings"
)

func Run() {
	srv := server.NewServer("ratelimit", settings.GrpcUnaryInterceptor(nil))

	s := settings.NewSettings()
	service := ratelimit.NewService(
		srv.Runtime(),
		redis.NewRateLimitCacheImpl(
			redis.NewPoolImpl(srv.Scope().Scope("redis_pool")),
			redis.NewTimeSourceImpl(),
			rand.New(redis.NewLockedSource(time.Now().Unix())),
			s.ExpirationJitterMaxSeconds),
		config.NewRateLimitConfigLoaderImpl(),
		srv.Scope().Scope("service"))

	srv.AddDebugHttpEndpoint(
		"/rlconfig",
		"print out the currently loaded configuration for debugging",
		func(writer http.ResponseWriter, request *http.Request) {
			io.WriteString(writer, service.GetCurrentConfig().Dump())
		})

	pb.RegisterRateLimitServiceServer(srv.GrpcServer(), service)
	srv.Start()
}
