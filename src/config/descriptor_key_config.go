package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
)

// DescriptorKeyConfig controls which descriptor entries include their runtime
// value in rate limit keys built by descriptorKey for Envoy limit overrides.
type DescriptorKeyConfig struct {
	defaultKeys map[string]struct{}
	domainKeys  map[string]map[string]struct{}
}

type yamlDescriptorKeyRoot struct {
	Default *yamlDescriptorKeyRule           `yaml:"default"`
	Domains map[string]yamlDescriptorKeyRule `yaml:"domains"`
}

type yamlDescriptorKeyRule struct {
	IncludeEntryValueForKeys []string `yaml:"include_entry_value_for_keys"`
}

// LoadDescriptorKeyConfig reads descriptor key rules from a YAML file.
// Returns nil if path is empty (legacy behavior: include all non-empty values).
func LoadDescriptorKeyConfig(path string) (*DescriptorKeyConfig, error) {
	if path == "" {
		return nil, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("descriptor key config: %w", err)
	}
	return ParseDescriptorKeyConfig(content)
}

func ParseDescriptorKeyConfig(content []byte) (*DescriptorKeyConfig, error) {
	var root yamlDescriptorKeyRoot
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("descriptor key config: %w", err)
	}

	ret := &DescriptorKeyConfig{
		defaultKeys: EntriesToSet(nil),
		domainKeys:  map[string]map[string]struct{}{},
	}

	if root.Default != nil {
		ret.defaultKeys = EntriesToSet(root.Default.IncludeEntryValueForKeys)
	}

	for domain, rule := range root.Domains {
		ret.domainKeys[domain] = EntriesToSet(rule.IncludeEntryValueForKeys)
	}

	return ret, nil
}

func EntriesToSet(entries []string) map[string]struct{} {
	entrySet := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entrySet[entry] = struct{}{}
	}
	return entrySet
}

// IncludeEntryValueForKey reports whether entry.Value should be appended for this domain/key.
// When cfg is nil, all non-empty values are included (backward compatible).
func (cfg *DescriptorKeyConfig) IncludeEntryValueForKey(domain, entry string) bool {
	if cfg == nil {
		return true
	}

	if domainKeys, ok := cfg.domainKeys[domain]; ok {
		_, include := domainKeys[entry]
		return include
	}

	_, include := cfg.defaultKeys[entry]
	return include
}
