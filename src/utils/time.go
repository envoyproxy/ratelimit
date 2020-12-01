package utils

import (
	"math/rand"
	"sync"
	"time"
)

// Interface for a rand Source for expiration jitter.
type JitterRandSource interface {
	// @return a non-negative pseudo-random 63-bit integer as an int64.
	Int63() int64
	// @param seed initializes pseudo-random generator to a deterministic state.
	Seed(seed int64)
}

type timeSourceImpl struct{}

func NewTimeSourceImpl() TimeSource {
	return &timeSourceImpl{}
}

func (this *timeSourceImpl) UnixNow() int64 {
	return time.Now().Unix()
}

// rand for jitter.
type lockedSource struct {
	lk  sync.Mutex
	src rand.Source
}

func NewLockedSource(seed int64) JitterRandSource {
	return &lockedSource{src: rand.NewSource(seed)}
}

func (r *lockedSource) Int63() (n int64) {
	r.lk.Lock()
	n = r.src.Int63()
	r.lk.Unlock()
	return
}

func (r *lockedSource) Seed(seed int64) {
	r.lk.Lock()
	r.src.Seed(seed)
	r.lk.Unlock()
}
