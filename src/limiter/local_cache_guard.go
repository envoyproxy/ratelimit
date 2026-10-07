package limiter

import (
	"sync"
	"sync/atomic"

	"github.com/coocood/freecache"
)

// LocalCacheGuard orders local cache writes against invalidations: an insert
// whose verdict predates a processed invalidation is suppressed, so it cannot
// resurrect the entry the invalidation deleted. The generation is global per
// replica — a false suppression only costs one extra Redis lookup.
type LocalCacheGuard struct {
	cache *freecache.Cache
	mu    sync.Mutex
	gen   atomic.Uint64
}

// NewLocalCacheGuard wraps the given cache; a nil cache yields a nil guard.
func NewLocalCacheGuard(cache *freecache.Cache) *LocalCacheGuard {
	if cache == nil {
		return nil
	}
	return &LocalCacheGuard{cache: cache}
}

func (this *LocalCacheGuard) Get(key []byte) ([]byte, error) {
	return this.cache.Get(key)
}

// GenSnapshot returns the invalidation generation; take it before the
// backend read that may lead to a poisoning insert.
func (this *LocalCacheGuard) GenSnapshot() uint64 {
	return this.gen.Load()
}

// SetIfUnchanged inserts the entry unless an invalidation was processed since
// the snapshot; it reports whether the insert happened.
func (this *LocalCacheGuard) SetIfUnchanged(key []byte, value []byte, expireSeconds int, genSnapshot uint64) (bool, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.gen.Load() != genSnapshot {
		return false, nil
	}
	return true, this.cache.Set(key, value, expireSeconds)
}

// Invalidate bumps the generation and deletes the entry, reporting whether it
// existed.
func (this *LocalCacheGuard) Invalidate(key []byte) bool {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.gen.Add(1)
	return this.cache.Del(key)
}
