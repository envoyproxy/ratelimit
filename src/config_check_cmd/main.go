package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"

	gostats "github.com/lyft/gostats"

	"github.com/envoyproxy/ratelimit/src/config"
)

func loadConfigs(allConfigs []config.RateLimitConfigToLoad, mergeDomainConfigs bool, descriptorKeyConfigPath string) {
	defer func() {
		err := recover()
		if err != nil {
			fmt.Printf("error loading rate limit configs: %s\n", err.(error).Error())
			os.Exit(1)
		}
	}()
	statsManager := stats.NewStatManager(gostats.NewStore(gostats.NewNullSink(), false), settings.NewSettings())
	descriptorKeyConfig, err := config.LoadDescriptorKeyConfig(descriptorKeyConfigPath)
	if err != nil {
		panic(err)
	}
	config.NewRateLimitConfigImpl(allConfigs, statsManager, mergeDomainConfigs, descriptorKeyConfig)
}

func main() {
	configDirectory := flag.String(
		"config_dir", "", "path to directory containing rate limit configs")
	mergeDomainConfigs := flag.Bool(
		"merge_domain_configs", false, "whether to merge configurations, referencing the same domain")
	descriptorKeyConfigPath := flag.String(
		"descriptor_key_config", "", "path to descriptor_key.yaml (optional)")
	flag.Parse()
	fmt.Printf("checking rate limit configs...\n")
	fmt.Printf("loading config directory: %s\n", *configDirectory)

	files, err := os.ReadDir(*configDirectory)
	if err != nil {
		fmt.Printf("error opening directory %s: %s\n", *configDirectory, err.Error())
		os.Exit(1)
	}

	allConfigs := []config.RateLimitConfigToLoad{}
	for _, file := range files {
		finalPath := filepath.Join(*configDirectory, file.Name())
		fmt.Printf("opening config file: %s\n", finalPath)
		bytes, err := os.ReadFile(finalPath)
		if err != nil {
			fmt.Printf("error reading file %s: %s\n", finalPath, err.Error())
			os.Exit(1)
		}
		configYaml := config.ConfigFileContentToYaml(finalPath, string(bytes))
		allConfigs = append(allConfigs, config.RateLimitConfigToLoad{Name: finalPath, ConfigYaml: configYaml})
	}

	loadConfigs(allConfigs, *mergeDomainConfigs, *descriptorKeyConfigPath)
	fmt.Printf("all rate limit configs ok\n")
}
