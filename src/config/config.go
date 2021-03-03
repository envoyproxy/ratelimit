package config

import (
	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	stats "github.com/lyft/gostats"
	"golang.org/x/net/context"
)

// Errors that may be raised during config parsing.
type RateLimitConfigError string

func (e RateLimitConfigError) Error() string {
	return string(e)
}

// Stats for an individual rate limit config entry.
type RateLimitStats struct {
	TotalHits               stats.Counter
	OverLimit               stats.Counter
	NearLimit               stats.Counter
	OverLimitWithLocalCache stats.Counter
	WithinLimit             stats.Counter
}

// Wrapper for an individual rate limit config entry which includes the defined limit and stats.
type RateLimit struct {
	FullKey string
	Stats   RateLimitStats
	Limit   *pb.RateLimitResponse_RateLimit
}

// Interface for interacting with a loaded rate limit config.
type RateLimitConfig interface {
	// Dump the configuration into string form for debugging.
	Dump() string

	// Get the configured limit for a rate limit descriptor.
	// @param ctx supplies the calling context.
	// @param domain supplies the domain to lookup the descriptor in.
	// @param descriptor supplies the descriptor to look up.
	// @return a rate limit to apply or nil if no rate limit is configured for the descriptor.
	GetLimit(ctx context.Context, domain string, descriptor *pb_struct.RateLimitDescriptor) *RateLimit
}

// Information for a config file to load into the aggregate config.
type RateLimitConfigToLoad struct {
	Name      string
	FileBytes string
}

// Interface for loading a configuration from a list of YAML files.
type RateLimitConfigLoader interface {
	// Load a new configuration from a list of YAML files.
	// @param configs supplies a list of full YAML files in string form.
	// @param statsScope supplies the stats scope to use for limit stats during runtime.
	// @return a new configuration.
	// @throws RateLimitConfigError if the configuration could not be created.
	Load(configs []RateLimitConfigToLoad, statsScope stats.Scope) RateLimitConfig
}
