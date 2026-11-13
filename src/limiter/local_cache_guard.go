package limiter

import (
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/coocood/freecache"
)

// LocalCacheGuard orders cache writes against invalidations using key-hashed generations.
type LocalCacheGuard struct {
	cache  *freecache.Cache
	shards [256]localCacheShard
}

type localCacheShard struct {
	mu  sync.Mutex
	gen atomic.Uint64
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

func (this *LocalCacheGuard) shard(key []byte) *localCacheShard {
	h := fnv.New32a()
	_, _ = h.Write(key)
	return &this.shards[h.Sum32()%uint32(len(this.shards))]
}

// GenSnapshot returns the key's generation; take it before the backend read.
func (this *LocalCacheGuard) GenSnapshot(key []byte) uint64 {
	return this.shard(key).gen.Load()
}

// SetIfUnchanged inserts the entry unless an invalidation was processed since
// the snapshot; it reports whether the insert happened.
func (this *LocalCacheGuard) SetIfUnchanged(key []byte, value []byte, expireSeconds int, genSnapshot uint64) (bool, error) {
	shard := this.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.gen.Load() != genSnapshot {
		return false, nil
	}
	return true, this.cache.Set(key, value, expireSeconds)
}

// Invalidate bumps the generation and deletes the entry, reporting whether it
// existed.
func (this *LocalCacheGuard) Invalidate(key []byte) bool {
	shard := this.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	shard.gen.Add(1)
	return this.cache.Del(key)
}
