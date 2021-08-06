package config

import (
	"regexp"
	"strings"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	logger "github.com/sirupsen/logrus"
)

func extractKeys(descriptorMap map[string]*rateLimitDescriptor) []string {
	var keys []string
	for k := range descriptorMap {
		split := strings.Split(k, "_")
		keys = append(keys, split[len(split)-1])
	}
	return keys
}

func checkRegex(descriptorMap map[string]*rateLimitDescriptor, descriptor *pb_struct.RateLimitDescriptor) (value string) {
	value = descriptor.Entries[0].Value
	keys := extractKeys(descriptorMap)
	for _, k := range keys {
		validID, err := regexp.Compile(k)
		if err != nil {
			logger.Infof("%s is not a regex expression", k)
		}
		regexMatch := validID.MatchString(descriptor.Entries[0].Value)
		logger.Debugf("Regex match %t", regexMatch)
		if len(descriptor.Entries) == 1 && regexMatch {
			value = k
			logger.Debugf("Rewrite descriptor")
		}
	}
	return
}
