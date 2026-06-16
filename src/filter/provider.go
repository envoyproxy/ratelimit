package filter

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

// Provider exposes the currently active IP/UID Filters. The read path
// (IPFilter / UIDFilter) is lock-free so it can be called at every-request
// rate without lock contention.
type Provider interface {
	IPFilter() Filter
	UIDFilter() Filter
	Stop()
}

// filterPair bundles the IP/UID filters so a single atomic.Load returns a
// self-consistent snapshot. The Provider API exposes IPFilter() and UIDFilter()
// as separate calls, so callers reading both across a reload may observe
// (old_ip, new_uid) — this is fine for the rate-limit cache, which treats
// the two lists independently. Callers that need both filters from the same
// snapshot must capture one Load themselves.
type filterPair struct {
	ip  Filter
	uid Filter
}

// --- static provider (env-var fallback) ---

type staticProvider struct {
	ip  Filter
	uid Filter
}

// NewStaticProvider returns a Provider whose filters never change. Used as
// the backward-compatible fallback when FILTER_CONFIG_PATH is empty (filters
// come from WHITELIST_IP_NET / BLACKLIST_IP_NET / WHITELIST_UID / BLACKLIST_UID
// at startup and stay frozen).
func NewStaticProvider(ip Filter, uid Filter) Provider {
	return &staticProvider{ip: ip, uid: uid}
}

func (s *staticProvider) IPFilter() Filter  { return s.ip }
func (s *staticProvider) UIDFilter() Filter { return s.uid }
func (s *staticProvider) Stop()             {}

// --- file provider (hot reload) ---

type fileProvider struct {
	path    string
	pair    atomic.Pointer[filterPair]
	watcher *fsnotify.Watcher
	log     *logger.Logger

	reloadSuccess stats.Counter
	reloadFailure stats.Counter

	stopOnce sync.Once
	done     chan struct{}
}

// NewFileProvider loads filters from path and hot-reloads on file changes.
// It watches the file's parent directory (Kubernetes ConfigMap mounts use a
// symlink that's swapped atomically — watching the file itself loses events).
//
// On initial load failure the function returns (nil, err) — the caller
// should treat this as a hard startup error. On subsequent reload failures
// (yaml/cidr errors or transient file removal) the old filter is kept,
// reload_failure counter increments, and an error is logged; the provider
// keeps running so a follow-up valid update can recover the system.
func NewFileProvider(path string, statsScope stats.Scope, log *logger.Logger) (Provider, error) {
	ip, uid, err := LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("filter: initial load: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("filter: create fsnotify watcher: %w", err)
	}

	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("filter: watch dir %s: %w", dir, err)
	}

	p := &fileProvider{
		path:          path,
		watcher:       watcher,
		log:           log,
		reloadSuccess: statsScope.NewCounter("reload_success"),
		reloadFailure: statsScope.NewCounter("reload_failure"),
		done:          make(chan struct{}),
	}
	p.pair.Store(&filterPair{ip: ip, uid: uid})

	go p.watch()
	return p, nil
}

func (p *fileProvider) IPFilter() Filter {
	if pair := p.pair.Load(); pair != nil {
		return pair.ip
	}
	return nil
}

func (p *fileProvider) UIDFilter() Filter {
	if pair := p.pair.Load(); pair != nil {
		return pair.uid
	}
	return nil
}

func (p *fileProvider) Stop() {
	p.stopOnce.Do(func() {
		close(p.done)
		_ = p.watcher.Close()
	})
}

// watch runs in a single goroutine for the provider's lifetime. Any event in
// the watched directory triggers a reload attempt — fsnotify's event types
// vary across platforms and across ConfigMap update patterns (write,
// atomic-rename, symlink swap), so reacting uniformly is simpler and the
// per-reload cost (one ReadFile + Unmarshal) is well below the noise floor.
func (p *fileProvider) watch() {
	for {
		select {
		case <-p.done:
			return
		case _, ok := <-p.watcher.Events:
			if !ok {
				return
			}
			p.reload()
		case err, ok := <-p.watcher.Errors:
			if !ok {
				return
			}
			// Watcher-internal errors (inotify queue overflow on Linux,
			// kqueue errors on macOS) don't invalidate the current filter
			// snapshot, but operators need the underlying cause visible —
			// a silent counter bump leaves a climbing reload_failure with
			// no log line, making host-level issues indistinguishable from
			// bad YAML pushes.
			p.log.WithError(err).Warnf("filter: fsnotify watcher error (path=%s)", p.path)
			p.reloadFailure.Inc()
		}
	}
}

func (p *fileProvider) reload() {
	ip, uid, err := LoadFromFile(p.path)
	if err != nil {
		// Keep the old filter — never clobber a working snapshot with bad
		// data. A transiently-missing ConfigMap or a half-written file must
		// not flip the gate to "deny everything".
		p.log.WithError(err).Warnf("filter: reload failed, keeping previous filter (path=%s)", p.path)
		p.reloadFailure.Inc()
		return
	}
	p.pair.Store(&filterPair{ip: ip, uid: uid})
	p.reloadSuccess.Inc()
	p.log.Infof("filter: reloaded from %s", p.path)
}
