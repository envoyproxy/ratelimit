package utils

// Interface for a time source.
type TimeSource interface {
	// @return the current unix time in seconds.
	UnixNow() int64
	// @return the current unix time in nanoseconds.
	UnixNanoNow() int64
}
