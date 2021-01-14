package utils

// Interface for a rand Source for expiration jitter.
type JitterRandSource interface {
	// @return a non-negative pseudo-random 63-bit integer as an int64.
	Int63() int64
	// @param seed initializes pseudo-random generator to a deterministic state.
	Seed(seed int64)
}
