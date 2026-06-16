package filter

import (
	"fmt"
	"net"
	"os"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

// FilterSnapshot is the YAML-decoded form of the filter file. Exported so
// callers (and tests) can construct filters from in-memory data without
// going through the filesystem.
type FilterSnapshot struct {
	IP  IPSection  `yaml:"ip"`
	UID UIDSection `yaml:"uid"`
}

// IPSection holds IP CIDR allow/deny lists.
type IPSection struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// UIDSection holds UID allow/deny lists.
type UIDSection struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// LoadFromFile reads YAML at path, validates all CIDRs, and constructs IP +
// UID Filters. On file-missing / YAML syntax error / CIDR format error it
// returns (nil, nil, err) — callers that hot-reload should keep the old
// filter on err rather than clobbering it.
func LoadFromFile(path string) (Filter, Filter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("filter: read %s: %w", path, err)
	}
	var snap FilterSnapshot
	if err := yaml.Unmarshal(data, &snap); err != nil {
		return nil, nil, fmt.Errorf("filter: parse yaml %s: %w", path, err)
	}
	return ParseSnapshot(snap)
}

// ParseSnapshot builds Filters from an already-decoded snapshot. Decoupled
// from filesystem IO so unit tests can target it without touching disk.
func ParseSnapshot(snap FilterSnapshot) (Filter, Filter, error) {
	allowIP, err := parseCIDRList(snap.IP.Allow)
	if err != nil {
		return nil, nil, fmt.Errorf("filter: invalid ip.allow: %w", err)
	}
	denyIP, err := parseCIDRList(snap.IP.Deny)
	if err != nil {
		return nil, nil, fmt.Errorf("filter: invalid ip.deny: %w", err)
	}
	return NewIPFilter(allowIP, denyIP), NewUIDFilter(uidSet(snap.UID.Allow), uidSet(snap.UID.Deny)), nil
}

func parseCIDRList(items []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(items))
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", s, err)
		}
		out = append(out, ipNet)
	}
	return out, nil
}

func uidSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	return out
}
