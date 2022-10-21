package config

import (
	rls_conf_v3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
)

// ConfigXdsProtoToYaml converts Xds Proto format to yamlRoot
func ConfigXdsProtoToYaml(xdsProto *rls_conf_v3.RateLimitConfig) *yamlRoot {
	return &yamlRoot{
		Domain:      xdsProto.Domain,
		Descriptors: rateLimitDescriptorsPbToYaml(xdsProto.Descriptors),
	}
}

func rateLimitDescriptorsPbToYaml(pb []*rls_conf_v3.RateLimitDescriptor) []yamlDescriptor {
	descriptors := make([]yamlDescriptor, len(pb))
	for i, d := range pb {
		descriptors[i] = yamlDescriptor{
			Key:         d.Key,
			Value:       d.Value,
			RateLimit:   rateLimitPolicyPbToYaml(d.RateLimit),
			Descriptors: rateLimitDescriptorsPbToYaml(d.Descriptors),
			ShadowMode:  d.ShadowMode,
		}
	}

	return descriptors
}

func rateLimitPolicyPbToYaml(pb *rls_conf_v3.RateLimitPolicy) *yamlRateLimit {
	if pb == nil {
		return nil
	}
	return &yamlRateLimit{
		RequestsPerUnit: pb.RequestsPerUnit,
		Unit:            pb.Unit,
		Unlimited:       pb.Unlimited,
		Name:            pb.Name,
		Replaces:        rateLimitReplacesPbToYaml(pb.Replaces),
	}
}

func rateLimitReplacesPbToYaml(pb []*rls_conf_v3.RateLimitReplace) []yamlReplaces {
	replaces := make([]yamlReplaces, len(pb))
	for i, r := range pb {
		replaces[i] = yamlReplaces{Name: r.Name}
	}
	return replaces
}
