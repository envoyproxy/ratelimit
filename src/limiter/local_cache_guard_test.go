package limiter

import (
	"fmt"
	"sync"
	"testing"

	"github.com/coocood/freecache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalCacheGuardShards(t *testing.T) {
	guard := NewLocalCacheGuard(freecache.NewCache(1024))
	key := []byte("key")
	var different, collision []byte
	for i := 0; i < 65536 && (different == nil || collision == nil); i++ {
		candidate := []byte(fmt.Sprintf("other-%d", i))
		if guard.shard(candidate) == guard.shard(key) {
			collision = candidate
		} else {
			different = candidate
		}
	}
	require.NotNil(t, different)
	require.NotNil(t, collision)

	for _, tt := range []struct {
		name     string
		refund   []byte
		inserted bool
	}{
		{"same key", key, false},
		{"different shard", different, true},
		{"hash collision", collision, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := guard.GenSnapshot(key)
			guard.Invalidate(tt.refund)
			inserted, err := guard.SetIfUnchanged(key, nil, 60, snapshot)
			require.NoError(t, err)
			assert.Equal(t, tt.inserted, inserted)
			inserted, err = guard.SetIfUnchanged(key, nil, 60, guard.GenSnapshot(key))
			require.NoError(t, err)
			assert.True(t, inserted)
			assert.True(t, guard.Invalidate(key))
		})
	}
}

func TestLocalCacheGuardClear(t *testing.T) {
	guard := NewLocalCacheGuard(freecache.NewCache(1024))
	var snapshots [256]uint64
	for round := 0; round < 2; round++ {
		for i := range snapshots {
			key := []byte{byte(i)}
			snapshots[i] = guard.GenSnapshot(key)
			inserted, err := guard.SetIfUnchanged(key, nil, 60, snapshots[i])
			require.NoError(t, err)
			require.True(t, inserted)
		}
		guard.Clear()
		for i, snapshot := range snapshots {
			key := []byte{byte(i)}
			_, err := guard.Get(key)
			require.ErrorIs(t, err, freecache.ErrNotFound)
			inserted, err := guard.SetIfUnchanged(key, nil, 60, snapshot)
			require.NoError(t, err)
			require.False(t, inserted)
			inserted, err = guard.SetIfUnchanged(key, nil, 60, guard.GenSnapshot(key))
			require.NoError(t, err)
			require.True(t, inserted)
		}
	}
}

func TestLocalCacheGuardConcurrentInvalidation(t *testing.T) {
	guard := NewLocalCacheGuard(freecache.NewCache(1024))
	key := []byte("key")
	for _, tt := range []struct {
		name       string
		invalidate func()
	}{
		{"key", func() { guard.Invalidate(key) }},
		{"clear", guard.Clear},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				snapshot := guard.GenSnapshot(key)
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					_, err := guard.SetIfUnchanged(key, nil, 60, snapshot)
					assert.NoError(t, err)
				}()
				go func() {
					defer wg.Done()
					<-start
					tt.invalidate()
				}()
				close(start)
				wg.Wait()
				_, err := guard.Get(key)
				require.ErrorIs(t, err, freecache.ErrNotFound)
			}
		})
	}
}
