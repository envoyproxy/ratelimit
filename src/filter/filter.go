package filter

import (
	"net"
)

// Action how to tackle the value
type Action int

// Action values
const (
	FilterActionNone Action = iota
	FilterActionAllow
	FilterActionDeny
	FilterActionError
)

// Filter denfines the entry value filter behavior
type Filter interface {
	Match(entryValue string) (action Action, reason string)
}

// NewIPFilter creates a new filter matching ip ranges
func NewIPFilter(allow, deny []*net.IPNet) Filter {
	if allow == nil {
		allow = make([]*net.IPNet, 0)
	}

	if deny == nil {
		deny = make([]*net.IPNet, 0)
	}

	return &ipFilter{
		Allowed: allow,
		Blocked: deny,
	}
}

type ipFilter struct {
	Allowed []*net.IPNet
	Blocked []*net.IPNet
}

func (f *ipFilter) Match(ip string) (Action, string) {
	if f == nil {
		return FilterActionNone, ""
	}

	acutalIP := net.ParseIP(ip)
	if acutalIP == nil {
		return FilterActionError, "can't parse remote ip: " + ip
	}

	for _, n := range f.Allowed {
		if n.Contains(acutalIP) {
			return FilterActionAllow, ""
		}
	}

	for _, n := range f.Blocked {
		if n.Contains(acutalIP) {
			return FilterActionDeny, ""
		}
	}

	return FilterActionNone, ""
}

// NewUIDFilter creates a new user id based filter
func NewUIDFilter(allow, deny map[string]struct{}) Filter {
	if allow == nil {
		allow = make(map[string]struct{})
	}

	if deny == nil {
		deny = make(map[string]struct{})
	}

	return &uidFilter{
		Allowed: allow,
		Blocked: deny,
	}
}

type uidFilter struct {
	Allowed map[string]struct{}
	Blocked map[string]struct{}
}

func (f *uidFilter) Match(uid string) (Action, string) {
	if f == nil {
		return FilterActionNone, ""
	}

	if _, ok := f.Allowed[uid]; ok {
		return FilterActionAllow, ""
	}

	if _, ok := f.Blocked[uid]; ok {
		return FilterActionDeny, ""
	}

	return FilterActionNone, ""
}
