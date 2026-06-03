package provider

import (
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/config"
)

func loadDescriptorKeyConfigOrPanic(path string) *config.DescriptorKeyConfig {
	cfg, err := config.LoadDescriptorKeyConfig(path)
	if err != nil {
		logger.Fatalf("failed to load descriptor key config from %q: %v", path, err)
	}
	if cfg != nil {
		logger.Infof("loaded descriptor key config from %q", path)
	}
	return cfg
}
