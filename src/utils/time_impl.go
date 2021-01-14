package utils

import (
	"time"
)

type timeSourceImpl struct{}

func (this *timeSourceImpl) UnixNow() int64 {
	return time.Now().Unix()
}

func (this *timeSourceImpl) UnixNanoNow() int64 {
	return time.Now().UnixNano()
}

func NewTimeSourceImpl() TimeSource {
	return &timeSourceImpl{}
}
