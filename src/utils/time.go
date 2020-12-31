package utils

import (
	"github.com/golang/protobuf/ptypes/duration"
	"time"
)

const secondToNanosecondRate = 1e9

func NanosecondsToSeconds(nanoseconds int64) int64 {
	return nanoseconds / secondToNanosecondRate
}

func NanosecondsToDuration(nanoseconds int64) *duration.Duration {
	nanos := nanoseconds
	secs := nanos / secondToNanosecondRate
	nanos -= secs * secondToNanosecondRate
	return &duration.Duration{Seconds: secs, Nanos: int32(nanos)}
}

func SecondsToNanoseconds(second int64) int64 {
	time.Now()
	return second * secondToNanosecondRate
}
