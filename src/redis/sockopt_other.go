//go:build !linux

package redis

import (
	"syscall"
	"time"
)

const tcpUserTimeoutSupported = false

func setTCPUserTimeout(_ syscall.RawConn, _ time.Duration) error {
	return nil
}
