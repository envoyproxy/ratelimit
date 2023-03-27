package config

import (
	"fmt"
	"strings"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"

	"github.com/envoyproxy/ratelimit/src/stats"
)

type yamlReplaces struct {
	Name string
}

type YamlRateLimit struct {
	RequestsPerUnit uint32 `yaml:"requests_per_unit"`
	Unit            string
	Unlimited       bool `yaml:"unlimited"`
	Name            string
	Replaces        []yamlReplaces
}

type YamlDescriptor struct {
	Key                               string
	Value                             string
	RateLimit                         *YamlRateLimit `yaml:"rate_limit"`
	Descriptors                       []YamlDescriptor
	ShadowMode                        bool `yaml:"shadow_mode"`
	IncludeMetricsForUnspecifiedValue bool `yaml:"detailed_metric"`
}

type YamlRoot struct {
	Domain      string
	Descriptors []YamlDescriptor
}

type rateLimitDescriptor struct {
	descriptors  map[string]*rateLimitDescriptor
	limit        *RateLimit
	wildcardKeys []string
}

type rateLimitDomain struct {
	rateLimitDescriptor
}

type rateLimitConfigImpl struct {
	domains            map[string]*rateLimitDomain
	statsManager       stats.Manager
	mergeDomainConfigs bool
}

var validKeys = map[string]bool{
	"domain":            true,
	"key":               true,
	"value":             true,
	"descriptors":       true,
	"rate_limit":        true,
	"unit":              true,
	"requests_per_unit": true,
	"unlimited":         true,
	"shadow_mode":       true,
	"name":              true,
	"replaces":          true,
	"detailed_metric":   true,
}

// Create a new rate limit config entry.
// @param requestsPerUnit supplies the requests per unit of time for the entry.
// @param unit supplies the unit of time for the entry.
// @param rlStats supplies the stats structure associated with the RateLimit
// @param unlimited supplies whether the rate limit is unlimited
// @return the new config entry.
func NewRateLimit(requestsPerUnit uint32, unit pb.RateLimitResponse_RateLimit_Unit, rlStats stats.RateLimitStats,
	unlimited bool, shadowMode bool, name string, replaces []string, includeValueInMetricWhenNotSpecified bool) *RateLimit {

	return &RateLimit{
		FullKey: rlStats.GetKey(),
		Stats:   rlStats,
		Limit: &pb.RateLimitResponse_RateLimit{
			RequestsPerUnit: requestsPerUnit,
			Unit:            unit,
		},
		Unlimited:                            unlimited,
		ShadowMode:                           shadowMode,
		Name:                                 name,
		Replaces:                             replaces,
		IncludeValueInMetricWhenNotSpecified: includeValueInMetricWhenNotSpecified,
	}
}

// Dump an individual descriptor for debugging purposes.
func (this *rateLimitDescriptor) dump() string {
	ret := ""
	if this.limit != nil {
		ret += fmt.Sprintf(
			"%s: unit=%s requests_per_unit=%d, shadow_mode: %t\n", this.limit.FullKey,
			this.limit.Limit.Unit.String(), this.limit.Limit.RequestsPerUnit, this.limit.ShadowMode)
	}
	for _, descriptor := range this.descriptors {
		ret += descriptor.dump()
	}
	return ret
}

// Create a new config error which includes the owning file.
// @param config supplies the config file that generated the error.
// @param err supplies the error string.
func newRateLimitConfigError(name string, err string) RateLimitConfigError {
	return RateLimitConfigError(fmt.Sprintf("%s: %s", name, err))
}

// Load a set of config descriptors from the YAML file and check the input.
// @param config supplies the config file that owns the descriptor.
// @param parentKey supplies the fully resolved key name that owns this config level.
// @param descriptors supplies the YAML descriptors to load.
// @param statsManager that owns the stats.Scope.
func (this *rateLimitDescriptor) loadDescriptors(config RateLimitConfigToLoad, parentKey string, descriptors []YamlDescriptor, statsManager stats.Manager) {
	for _, descriptorConfig := range descriptors {
		if descriptorConfig.Key == "" {
			panic(newRateLimitConfigError(config.Name, "descriptor has empty key"))
		}

		// Value is optional, so the final key for the map is either the key only or key_value.
		finalKey := descriptorConfig.Key
		if descriptorConfig.Value != "" {
			finalKey += "_" + descriptorConfig.Value
		}

		newParentKey := parentKey + finalKey
		if _, present := this.descriptors[finalKey]; present {
			panic(newRateLimitConfigError(
				config.Name, fmt.Sprintf("duplicate descriptor composite key '%s'", newParentKey)))
		}

		var rateLimit *RateLimit = nil
		var rateLimitDebugString string = ""
		if descriptorConfig.RateLimit != nil {
			unlimited := descriptorConfig.RateLimit.Unlimited

			value, present :=
				pb.RateLimitResponse_RateLimit_Unit_value[strings.ToUpper(descriptorConfig.RateLimit.Unit)]
			validUnit := present && value != int32(pb.RateLimitResponse_RateLimit_UNKNOWN)

			if unlimited {
				if validUnit {
					panic(newRateLimitConfigError(
						config.Name,
						fmt.Sprintf("should not specify rate limit unit when unlimited")))
				}
			} else if !validUnit {
				panic(newRateLimitConfigError(
					config.Name,
					fmt.Sprintf("invalid rate limit unit '%s'", descriptorConfig.RateLimit.Unit)))
			}

			replaces := make([]string, len(descriptorConfig.RateLimit.Replaces))
			for i, e := range descriptorConfig.RateLimit.Replaces {
				replaces[i] = e.Name
			}

			rateLimit = NewRateLimit(
				descriptorConfig.RateLimit.RequestsPerUnit, pb.RateLimitResponse_RateLimit_Unit(value),
				statsManager.NewStats(newParentKey), unlimited, descriptorConfig.ShadowMode,
				descriptorConfig.RateLimit.Name, replaces, descriptorConfig.IncludeMetricsForUnspecifiedValue,
			)
			rateLimitDebugString = fmt.Sprintf(
				" ratelimit={requests_per_unit=%d, unit=%s, unlimited=%t, shadow_mode=%t}", rateLimit.Limit.RequestsPerUnit,
				rateLimit.Limit.Unit.String(), rateLimit.Unlimited, rateLimit.ShadowMode)

			for _, replaces := range descriptorConfig.RateLimit.Replaces {
				if replaces.Name == "" {
					panic(newRateLimitConfigError(config.Name, "should not have an empty replaces entry"))
				}
				if replaces.Name == descriptorConfig.RateLimit.Name {
					panic(newRateLimitConfigError(config.Name, "replaces should not contain name of same descriptor"))
				}
			}
		}

		logger.Debugf(
			"loading descriptor: key=%s%s", newParentKey, rateLimitDebugString)
		newDescriptor := &rateLimitDescriptor{map[string]*rateLimitDescriptor{}, rateLimit, nil}
		newDescriptor.loadDescriptors(config, newParentKey+".", descriptorConfig.Descriptors, statsManager)
		this.descriptors[finalKey] = newDescriptor

		// Preload keys ending with "*" symbol.
		if finalKey[len(finalKey)-1:] == "*" {
			this.wildcardKeys = append(this.wildcardKeys, finalKey)
		}
	}
}

// Validate a YAML config file's keys.
// @param config specifies the file contents to load.
// @param any specifies the yaml file and a map.
func validateYamlKeys(fileName string, config_map map[interface{}]interface{}) {
	for k, v := range config_map {
		if _, ok := k.(string); !ok {
			errorText := fmt.Sprintf("config error, key is not of type string: %v", k)
			logger.Debugf(errorText)
			panic(newRateLimitConfigError(fileName, errorText))
		}
		if _, ok := validKeys[k.(string)]; !ok {
			errorText := fmt.Sprintf("config error, unknown key '%s'", k)
			logger.Debugf(errorText)
			panic(newRateLimitConfigError(fileName, errorText))
		}
		switch v := v.(type) {
		case []interface{}:
			for _, e := range v {
				if _, ok := e.(map[interface{}]interface{}); !ok {
					errorText := fmt.Sprintf("config error, yaml file contains list of type other than map: %v", e)
					logger.Debugf(errorText)
					panic(newRateLimitConfigError(fileName, errorText))
				}
				element := e.(map[interface{}]interface{})
				validateYamlKeys(fileName, element)
			}
		case map[interface{}]interface{}:
			validateYamlKeys(fileName, v)
		// string is a leaf type in ratelimit config. No need to keep validating.
		case string:
		// int is a leaf type in ratelimit config. No need to keep validating.
		case int:
		// bool is a leaf type in ratelimit config. No need to keep validating.
		case bool:
		// nil case is an incorrectly formed yaml. However, because this function's purpose is to validate
		// the yaml's keys we don't panic here.
		case nil:
		default:
			errorText := fmt.Sprintf("error checking config")
			logger.Debugf(errorText)
			panic(newRateLimitConfigError(fileName, errorText))
		}
	}
}

// Load a single YAML config into the global config.
// @param config specifies the yamlRoot struct to load.
func (this *rateLimitConfigImpl) loadConfig(config RateLimitConfigToLoad) {
	root := config.ConfigYaml

	if root.Domain == "" {
		panic(newRateLimitConfigError(config.Name, "config file cannot have empty domain"))
	}

	if _, present := this.domains[root.Domain]; present {
		if !this.mergeDomainConfigs {
			panic(newRateLimitConfigError(
				config.Name, fmt.Sprintf("duplicate domain '%s' in config file", root.Domain)))
		}

		logger.Debugf("patching domain: %s", root.Domain)
		this.domains[root.Domain].loadDescriptors(config, root.Domain+".", root.Descriptors, this.statsManager)
		return
	}

	logger.Debugf("loading domain: %s", root.Domain)
	newDomain := &rateLimitDomain{rateLimitDescriptor{map[string]*rateLimitDescriptor{}, nil, nil}}
	newDomain.loadDescriptors(config, root.Domain+".", root.Descriptors, this.statsManager)
	this.domains[root.Domain] = newDomain
}

func (this *rateLimitConfigImpl) Dump() string {
	ret := ""
	for _, domain := range this.domains {
		ret += domain.dump()
	}

	return ret
}

func (this *rateLimitConfigImpl) GetLimit(
	ctx context.Context, domain string, descriptor *pb_struct.RateLimitDescriptor) *RateLimit {

	logger.Debugf("starting get limit lookup")
	var rateLimit *RateLimit = nil
	value := this.domains[domain]
	if value == nil {
		logger.Debugf("unknown domain '%s'", domain)
		return rateLimit
	}

	if descriptor.GetLimit() != nil {
		rateLimitKey := descriptorKey(domain, descriptor)
		rateLimitOverrideUnit := pb.RateLimitResponse_RateLimit_Unit(descriptor.GetLimit().GetUnit())
		// When limit override is provided by envoy config, we don't want to enable shadow_mode
		rateLimit = NewRateLimit(
			descriptor.GetLimit().GetRequestsPerUnit(),
			rateLimitOverrideUnit,
			this.statsManager.NewStats(rateLimitKey),
			false,
			false,
			"",
			[]string{},
			false,
		)
		return rateLimit
	}

	descriptorsMap := value.descriptors
	for i, entry := range descriptor.Entries {
		// First see if key_value is in the map. If that isn't in the map we look for just key
		// to check for a default value.
		finalKey := entry.Key + "_" + entry.Value
		logger.Debugf("looking up key: %s", finalKey)
		nextDescriptor := descriptorsMap[finalKey]

		if nextDescriptor == nil && len(value.wildcardKeys) > 0 {
			for _, wildcardKey := range value.wildcardKeys {
				if strings.HasPrefix(finalKey, strings.TrimSuffix(wildcardKey, "*")) {
					nextDescriptor = descriptorsMap[wildcardKey]
					break
				}
			}
		}

		if nextDescriptor == nil {
			finalKey = entry.Key
			logger.Debugf("looking up key: %s", finalKey)
			nextDescriptor = descriptorsMap[finalKey]
		}

		if nextDescriptor != nil && nextDescriptor.limit != nil {
			logger.Debugf("found rate limit: %s", finalKey)

			if i == len(descriptor.Entries)-1 {
				rateLimit = nextDescriptor.limit
			} else {
				logger.Debugf("request depth does not match config depth, there are more entries in the request's descriptor")
			}
		}

		if nextDescriptor != nil && len(nextDescriptor.descriptors) > 0 {
			logger.Debugf("iterating to next level")
			descriptorsMap = nextDescriptor.descriptors
		} else {
			if rateLimit != nil && rateLimit.IncludeValueInMetricWhenNotSpecified {
				rateLimit = NewRateLimit(rateLimit.Limit.RequestsPerUnit, rateLimit.Limit.Unit, this.statsManager.NewStats(rateLimit.FullKey+"_"+entry.Value), rateLimit.Unlimited, rateLimit.ShadowMode, rateLimit.Name, rateLimit.Replaces, false)
			}

			break
		}
	}

	return rateLimit
}

func (this *rateLimitConfigImpl) IsEmptyDomains() bool {
	return len(this.domains) == 0
}

func descriptorKey(domain string, descriptor *pb_struct.RateLimitDescriptor) string {
	rateLimitKey := ""
	for _, entry := range descriptor.Entries {
		if rateLimitKey != "" {
			rateLimitKey += "."
		}
		rateLimitKey += entry.Key
		if entry.Value != "" {
			rateLimitKey += "_" + entry.Value
		}
	}
	return domain + "." + rateLimitKey
}

// ConfigFileContentToYaml converts a single YAML (string content) into yamlRoot struct with validating yaml keys.
// @param fileName specifies the name of the file.
// @param content specifies the string content of the yaml file.
func ConfigFileContentToYaml(fileName, content string) *YamlRoot {
	// validate keys in config with generic map
	any := map[interface{}]interface{}{}
	err := yaml.Unmarshal([]byte(content), &any)
	if err != nil {
		errorText := fmt.Sprintf("error loading config file: %s", err.Error())
		logger.Debugf(errorText)
		panic(newRateLimitConfigError(fileName, errorText))
	}
	validateYamlKeys(fileName, any)

	var root YamlRoot
	err = yaml.Unmarshal([]byte(content), &root)
	if err != nil {
		errorText := fmt.Sprintf("error loading config file: %s", err.Error())
		logger.Debugf(errorText)
		panic(newRateLimitConfigError(fileName, errorText))
	}

	return &root
}

// Create rate limit config from a list of input YAML files.
// @param configs specifies a list of YAML files to load.
// @param stats supplies the stats scope to use for limit stats during runtime.
// @param mergeDomainConfigs defines whether multiple configurations referencing the same domain will be merged or rejected throwing an error.
// @return a new config.
func NewRateLimitConfigImpl(
	configs []RateLimitConfigToLoad, statsManager stats.Manager, mergeDomainConfigs bool) RateLimitConfig {

	ret := &rateLimitConfigImpl{map[string]*rateLimitDomain{}, statsManager, mergeDomainConfigs}
	for _, config := range configs {
		ret.loadConfig(config)
	}

	return ret
}

type rateLimitConfigLoaderImpl struct{}

func (this *rateLimitConfigLoaderImpl) Load(
	configs []RateLimitConfigToLoad, statsManager stats.Manager, mergeDomainConfigs bool) RateLimitConfig {

	return NewRateLimitConfigImpl(configs, statsManager, mergeDomainConfigs)
}

// @return a new default config loader implementation.
func NewRateLimitConfigLoaderImpl() RateLimitConfigLoader {
	return &rateLimitConfigLoaderImpl{}
}
