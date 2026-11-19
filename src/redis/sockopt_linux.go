package redis

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const tcpUserTimeoutSupported = true

// setTCPUserTimeout sets TCP_USER_TIMEOUT on the socket: the kernel closes the
// connection when data it sent stays unacknowledged for longer than timeout,
// instead of retransmitting for net.ipv4.tcp_retries2 (about 15 minutes by
// default). Keepalive probes cannot detect this case, since they are only
// sent on idle connections.
func setTCPUserTimeout(c syscall.RawConn, timeout time.Duration) error {
	var sockErr error
	err := c.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(timeout.Milliseconds()))
	})
	if err != nil {
		return err
	}
	return sockErr
}
