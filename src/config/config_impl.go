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
	Key            string
	Value          string
	RateLimit      *YamlRateLimit `yaml:"rate_limit"`
	Descriptors    []YamlDescriptor
	ShadowMode     bool `yaml:"shadow_mode"`
	DetailedMetric bool `yaml:"detailed_metric"`
	ValueToMetric  bool `yaml:"value_to_metric"`
	ShareThreshold bool `yaml:"share_threshold"`
}

type YamlRoot struct {
	Domain      string
	Descriptors []YamlDescriptor
}

type rateLimitDescriptor struct {
	descriptors     map[string]*rateLimitDescriptor
	limit           *RateLimit
	wildcardKeys    []string
	valueToMetric   bool
	shareThreshold  bool
	wildcardPattern string // stores the wildcard pattern when share_threshold is true
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
	"value_to_metric":   true,
	"share_threshold":   true,
}

// Create a new rate limit config entry.
// @param requestsPerUnit supplies the requests per unit of time for the entry.
// @param unit supplies the unit of time for the entry.
// @param rlStats supplies the stats structure associated with the RateLimit
// @param unlimited supplies whether the rate limit is unlimited
// @return the new config entry.
func NewRateLimit(requestsPerUnit uint32, unit pb.RateLimitResponse_RateLimit_Unit, rlStats stats.RateLimitStats,
	unlimited bool, shadowMode bool, name string, replaces []string, detailedMetric bool,
) *RateLimit {
	return &RateLimit{
		FullKey: rlStats.GetKey(),
		Stats:   rlStats,
		Limit: &pb.RateLimitResponse_RateLimit{
			RequestsPerUnit: requestsPerUnit,
			Unit:            unit,
			Name:            name,
		},
		Unlimited:                unlimited,
		ShadowMode:               shadowMode,
		Name:                     name,
		Replaces:                 replaces,
		DetailedMetric:           detailedMetric,
		ShareThresholdKeyPattern: nil,
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

			value, present := pb.RateLimitResponse_RateLimit_Unit_value[strings.ToUpper(descriptorConfig.RateLimit.Unit)]
			validUnit := present && value != int32(pb.RateLimitResponse_RateLimit_UNKNOWN)

			if unlimited {
				if validUnit {
					panic(newRateLimitConfigError(
						config.Name,
						"should not specify rate limit unit when unlimited"))
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
				descriptorConfig.RateLimit.Name, replaces, descriptorConfig.DetailedMetric,
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

		// Validate share_threshold can only be used with wildcards
		if descriptorConfig.ShareThreshold {
			if len(finalKey) == 0 || finalKey[len(finalKey)-1:] != "*" {
				panic(newRateLimitConfigError(
					config.Name,
					fmt.Sprintf("share_threshold can only be used with wildcard values (ending with '*'), but found key '%s'", finalKey)))
			}
		}

		// Store wildcard pattern if share_threshold is enabled
		var wildcardPattern string = ""
		if descriptorConfig.ShareThreshold && len(finalKey) > 0 && finalKey[len(finalKey)-1:] == "*" {
			wildcardPattern = finalKey
		}

		// Preload keys ending with "*" symbol.
		if finalKey[len(finalKey)-1:] == "*" {
			this.wildcardKeys = append(this.wildcardKeys, finalKey)
		}

		logger.Debugf(
			"loading descriptor: key=%s%s", newParentKey, rateLimitDebugString)
		newDescriptor := &rateLimitDescriptor{
			descriptors:     map[string]*rateLimitDescriptor{},
			limit:           rateLimit,
			wildcardKeys:    nil,
			valueToMetric:   descriptorConfig.ValueToMetric,
			shareThreshold:  descriptorConfig.ShareThreshold,
			wildcardPattern: wildcardPattern,
		}
		newDescriptor.loadDescriptors(config, newParentKey+".", descriptorConfig.Descriptors, statsManager)
		this.descriptors[finalKey] = newDescriptor
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
			errorText := "error checking config"
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
	newDomain := &rateLimitDomain{rateLimitDescriptor{
		descriptors:     map[string]*rateLimitDescriptor{},
		limit:           nil,
		wildcardKeys:    nil,
		valueToMetric:   false,
		shareThreshold:  false,
		wildcardPattern: "",
	}}
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
	ctx context.Context, domain string, descriptor *pb_struct.RateLimitDescriptor,
) *RateLimit {
	logger.Debugf("starting get limit lookup")
	var rateLimit *RateLimit = nil
	value := this.domains[domain]
	if value == nil {
		logger.Debugf("unknown domain '%s'", domain)
		domainStats := this.statsManager.NewDomainStats(domain)
		domainStats.NotFound.Inc()
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
	prevDescriptor := &value.rateLimitDescriptor

	// Build detailed metric as we traverse the list of descriptors
	var detailedMetricFullKey strings.Builder
	detailedMetricFullKey.WriteString(domain)

	// Build value_to_metric-enhanced metric key as we traverse
	var valueToMetricFullKey strings.Builder
	valueToMetricFullKey.WriteString(domain)

	// Track share_threshold patterns for entries matched via wildcard (using indexes)
	// This allows share_threshold to work when wildcard has nested descriptors
	var shareThresholdPatterns map[int]string

	for i, entry := range descriptor.Entries {
		// First see if key_value is in the map. If that isn't in the map we look for just key
		// to check for a default value.
		finalKey := entry.Key + "_" + entry.Value

		detailedMetricFullKey.WriteString(".")
		detailedMetricFullKey.WriteString(finalKey)

		logger.Debugf("looking up key: %s", finalKey)
		nextDescriptor := descriptorsMap[finalKey]
		var matchedWildcardKey string

		if nextDescriptor == nil && len(prevDescriptor.wildcardKeys) > 0 {
			for _, wildcardKey := range prevDescriptor.wildcardKeys {
				if strings.HasPrefix(finalKey, strings.TrimSuffix(wildcardKey, "*")) {
					nextDescriptor = descriptorsMap[wildcardKey]
					matchedWildcardKey = wildcardKey
					break
				}
			}
		}

		matchedUsingValue := nextDescriptor != nil
		if nextDescriptor == nil {
			finalKey = entry.Key
			logger.Debugf("looking up key: %s", finalKey)
			nextDescriptor = descriptorsMap[finalKey]
			matchedUsingValue = false
		}

		// Track share_threshold pattern when matching via wildcard, even if no rate_limit at this level
		if matchedWildcardKey != "" && nextDescriptor != nil && nextDescriptor.shareThreshold && nextDescriptor.wildcardPattern != "" {
			// Extract the value part from the wildcard pattern (e.g., "key_files*" -> "files*")
			if shareThresholdPatterns == nil {
				shareThresholdPatterns = make(map[int]string)
			}

			wildcardValue := strings.TrimPrefix(nextDescriptor.wildcardPattern, entry.Key+"_")
			shareThresholdPatterns[i] = wildcardValue
			logger.Debugf("tracking share_threshold for entry index %d (key %s), wildcard pattern %s", i, entry.Key, wildcardValue)
		}

		// Build value_to_metric metrics path for this level
		valueToMetricFullKey.WriteString(".")
		if nextDescriptor != nil {
			// Check if share_threshold is enabled for this entry
			hasShareThreshold := shareThresholdPatterns[i] != ""
			if matchedWildcardKey != "" {
				// If share_threshold: always use wildcard pattern with * (e.g., "foo*")
				// Else if value_to_metric: use actual runtime value (e.g., "foo1")
				// Else: use wildcard pattern with * (e.g., "foo*")
				if hasShareThreshold {
					// share_threshold: always use wildcard pattern with *
					valueToMetricFullKey.WriteString(entry.Key)
					valueToMetricFullKey.WriteString("_")
					valueToMetricFullKey.WriteString(shareThresholdPatterns[i])
				} else if nextDescriptor.valueToMetric {
					valueToMetricFullKey.WriteString(entry.Key)
					if entry.Value != "" {
						valueToMetricFullKey.WriteString("_")
						valueToMetricFullKey.WriteString(entry.Value)
					}
				} else {
					// No value_to_metric on this descriptor: preserve wildcard pattern
					// Extract the value part from the matched wildcard key (e.g., "key_foo*" -> "foo*")
					wildcardValue := strings.TrimPrefix(matchedWildcardKey, entry.Key+"_")
					valueToMetricFullKey.WriteString(entry.Key)
					valueToMetricFullKey.WriteString("_")
					valueToMetricFullKey.WriteString(wildcardValue)
				}
			} else if matchedUsingValue {
				// Matched explicit key+value in config
				// If share_threshold: use wildcard pattern with *
				if hasShareThreshold {
					valueToMetricFullKey.WriteString(entry.Key)
					valueToMetricFullKey.WriteString("_")
					valueToMetricFullKey.WriteString(shareThresholdPatterns[i])
				} else {
					valueToMetricFullKey.WriteString(entry.Key)
					if entry.Value != "" {
						valueToMetricFullKey.WriteString("_")
						valueToMetricFullKey.WriteString(entry.Value)
					}
				}
			} else {
				// Matched default key (no value) in config
				// share_threshold can't apply here (only works with wildcards)
				if nextDescriptor.valueToMetric {
					valueToMetricFullKey.WriteString(entry.Key)
					if entry.Value != "" {
						valueToMetricFullKey.WriteString("_")
						valueToMetricFullKey.WriteString(entry.Value)
					}
				} else {
					valueToMetricFullKey.WriteString(entry.Key)
				}
			}
		} else {
			// No next descriptor found; still append something deterministic
			valueToMetricFullKey.WriteString(entry.Key)
		}

		if nextDescriptor != nil && nextDescriptor.limit != nil {
			logger.Debugf("found rate limit: %s", finalKey)

			if i == len(descriptor.Entries)-1 {
				// Create a copy of the rate limit to avoid modifying the shared object
				originalLimit := nextDescriptor.limit
				rateLimit = &RateLimit{
					FullKey:        originalLimit.FullKey,
					Stats:          originalLimit.Stats,
					Limit:          originalLimit.Limit,
					Unlimited:      originalLimit.Unlimited,
					ShadowMode:     originalLimit.ShadowMode,
					Name:           originalLimit.Name,
					Replaces:       originalLimit.Replaces,
					DetailedMetric: originalLimit.DetailedMetric,
					// Initialize ShareThresholdKeyPattern with correct length, empty strings for entries without share_threshold
					ShareThresholdKeyPattern: nil,
				}
				// Apply all tracked share_threshold patterns when we find the rate_limit
				// This works whether the rate_limit is at the wildcard level or deeper
				// Only entries with share_threshold will have non-empty patterns
				if len(shareThresholdPatterns) > 0 {
					rateLimit.ShareThresholdKeyPattern = make([]string, len(descriptor.Entries))
				}

				for idx, pattern := range shareThresholdPatterns {
					rateLimit.ShareThresholdKeyPattern[idx] = pattern
					logger.Debugf("share_threshold enabled for entry index %d, using wildcard pattern %s", idx, pattern)
				}
			} else {
				logger.Debugf("request depth does not match config depth, there are more entries in the request's descriptor")
			}
		}

		if nextDescriptor != nil && len(nextDescriptor.descriptors) > 0 {
			logger.Debugf("iterating to next level")
			descriptorsMap = nextDescriptor.descriptors
		} else {
			if rateLimit != nil && rateLimit.DetailedMetric {
				// Preserve ShareThresholdKeyPattern when recreating rate limit
				originalShareThresholdKeyPattern := rateLimit.ShareThresholdKeyPattern
				rateLimit = NewRateLimit(rateLimit.Limit.RequestsPerUnit, rateLimit.Limit.Unit, this.statsManager.NewStats(rateLimit.FullKey), rateLimit.Unlimited, rateLimit.ShadowMode, rateLimit.Name, rateLimit.Replaces, rateLimit.DetailedMetric)
				rateLimit.ShareThresholdKeyPattern = originalShareThresholdKeyPattern
			}

			break
		}
		prevDescriptor = nextDescriptor
	}

	// Replace metric with detailed metric, if leaf descriptor is detailed.
	// If share_threshold is enabled, always use wildcard pattern with *
	if rateLimit != nil && rateLimit.DetailedMetric {
		// Check if any entry has share_threshold enabled
		hasShareThreshold := rateLimit.ShareThresholdKeyPattern != nil && len(rateLimit.ShareThresholdKeyPattern) > 0
		if hasShareThreshold {
			// Build metric key with wildcard pattern (including *) for entries with share_threshold
			var shareThresholdMetricKey strings.Builder
			shareThresholdMetricKey.WriteString(domain)
			for i, entry := range descriptor.Entries {
				shareThresholdMetricKey.WriteString(".")
				if i < len(rateLimit.ShareThresholdKeyPattern) && rateLimit.ShareThresholdKeyPattern[i] != "" {
					shareThresholdMetricKey.WriteString(entry.Key)
					shareThresholdMetricKey.WriteString("_")
					shareThresholdMetricKey.WriteString(rateLimit.ShareThresholdKeyPattern[i])
				} else {
					// Include full key_value for entries without share_threshold
					shareThresholdMetricKey.WriteString(entry.Key)
					if entry.Value != "" {
						shareThresholdMetricKey.WriteString("_")
						shareThresholdMetricKey.WriteString(entry.Value)
					}
				}
			}
			shareThresholdKey := shareThresholdMetricKey.String()
			rateLimit.FullKey = shareThresholdKey
			rateLimit.Stats = this.statsManager.NewStats(shareThresholdKey)
		} else {
			detailedKey := detailedMetricFullKey.String()
			rateLimit.FullKey = detailedKey
			rateLimit.Stats = this.statsManager.NewStats(detailedKey)
		}
	}

	// If not using detailed metric, but any value_to_metric path produced a different key,
	// override stats to use the value_to_metric-enhanced key
	if rateLimit != nil && !rateLimit.DetailedMetric {
		enhancedKey := valueToMetricFullKey.String()
		if enhancedKey != rateLimit.FullKey {
			// Recreate to ensure a clean stats struct, then set to enhanced stats
			originalShareThresholdKeyPattern := rateLimit.ShareThresholdKeyPattern
			rateLimit = NewRateLimit(rateLimit.Limit.RequestsPerUnit, rateLimit.Limit.Unit, this.statsManager.NewStats(rateLimit.FullKey), rateLimit.Unlimited, rateLimit.ShadowMode, rateLimit.Name, rateLimit.Replaces, rateLimit.DetailedMetric)
			rateLimit.ShareThresholdKeyPattern = originalShareThresholdKeyPattern
			rateLimit.Stats = this.statsManager.NewStats(enhancedKey)
			rateLimit.FullKey = enhancedKey
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
	configs []RateLimitConfigToLoad, statsManager stats.Manager, mergeDomainConfigs bool,
) RateLimitConfig {
	ret := &rateLimitConfigImpl{map[string]*rateLimitDomain{}, statsManager, mergeDomainConfigs}
	for _, config := range configs {
		ret.loadConfig(config)
	}

	return ret
}

type rateLimitConfigLoaderImpl struct{}

func (this *rateLimitConfigLoaderImpl) Load(
	configs []RateLimitConfigToLoad, statsManager stats.Manager, mergeDomainConfigs bool,
) RateLimitConfig {
	return NewRateLimitConfigImpl(configs, statsManager, mergeDomainConfigs)
}

// @return a new default config loader implementation.
func NewRateLimitConfigLoaderImpl() RateLimitConfigLoader {
	return &rateLimitConfigLoaderImpl{}
}
