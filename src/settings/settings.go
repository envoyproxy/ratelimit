package settings

import (
	"github.com/kelseyhightower/envconfig"
	"google.golang.org/grpc"
	"net"
	"strings"
)

type Settings struct {
	// runtime options
	GrpcUnaryInterceptor grpc.ServerOption
	WhiteListIPNetList [] *net.IPNet
	// env config
	Port                       int    `envconfig:"PORT" default:"8080"`
	GrpcPort                   int    `envconfig:"GRPC_PORT" default:"8081"`
	DebugPort                  int    `envconfig:"DEBUG_PORT" default:"6070"`
	UseStatsd                  bool   `envconfig:"USE_STATSD" default:"true"`
	StatsdHost                 string `envconfig:"STATSD_HOST" default:"localhost"`
	StatsdPort                 int    `envconfig:"STATSD_PORT" default:"8125"`
	RuntimePath                string `envconfig:"RUNTIME_ROOT" default:"/srv/runtime_data/current"`
	RuntimeSubdirectory        string `envconfig:"RUNTIME_SUBDIRECTORY"`
	LogLevel                   string `envconfig:"LOG_LEVEL" default:"WARN"`
	ForceFlag				   bool   `envconfig:"FORCE_FLAG" default:"false"`
	RedisSocketType            string `envconfig:"REDIS_SOCKET_TYPE" default:"tcp"`
	RedisUrl                   string `envconfig:"REDIS_URL" default:"redis:6379"`
	RedisPassword			   string `envconfig:"REDIS_PASSWORD" default:"toor333666"`
	RedisPoolSize              int    `envconfig:"REDIS_POOL_SIZE" default:"10"`
	RedisPerSecond             bool   `envconfig:"REDIS_PERSECOND" default:"false"`
	RedisPerSecondSocketType   string `envconfig:"REDIS_PERSECOND_SOCKET_TYPE" default:"unix"`
	RedisPerSecondUrl          string `envconfig:"REDIS_PERSECOND_URL" default:"/var/run/nutcracker/ratelimitpersecond.sock"`
	RedisPerSecondPoolSize     int    `envconfig:"REDIS_PERSECOND_POOL_SIZE" default:"10"`
	ExpirationJitterMaxSeconds int64  `envconfig:"EXPIRATION_JITTER_MAX_SECONDS" default:"300"`
	WhiteListIPNetString             string `envconfig:"WHITELIST_IP_NET" default:"192.168.0.0/24,10.0.0.0/8"`
}

type Option func(*Settings)

var settings *Settings = nil

func NewSettings() Settings {
	if settings != nil {
		return *settings
	}
	var s Settings

	err := envconfig.Process("", &s)
	if err != nil {
		panic(err)
	}
	s.WhiteListIPNetList, err = ParseIPNetString(s.WhiteListIPNetString)
	if err != nil {
		panic(err)
	}
	settings = &s
	return s
}

func GrpcUnaryInterceptor(i grpc.UnaryServerInterceptor) Option {
	return func(s *Settings) {
		s.GrpcUnaryInterceptor = grpc.UnaryInterceptor(i)
	}
}

func ParseIPNetString(IPNetString string) ([]*net.IPNet,error) {
	ipNetStringList := strings.Split(IPNetString, ",")
	var result []*net.IPNet
	for _, ipNetString := range ipNetStringList {
		_, ipNet, err := net.ParseCIDR(ipNetString)
		if err != nil {
			return nil, err
		}
		result = append(result, ipNet)
	}
	return result, nil
}