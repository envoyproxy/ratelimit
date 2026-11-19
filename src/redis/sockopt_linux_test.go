package redis

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestNetDialerSetsTCPUserTimeout(t *testing.T) {
	srv := newFakeRedis(t, nil)
	d := newNetDialer(time.Second, clientOptions{tcpUserTimeout: 5 * time.Second}, false, nil)

	conn, err := d.DialContext(context.Background(), "tcp", srv.addr())
	require.NoError(t, err)
	defer conn.Close()

	raw, err := conn.(*net.TCPConn).SyscallConn()
	require.NoError(t, err)
	var got int
	var sockErr error
	require.NoError(t, raw.Control(func(fd uintptr) {
		got, sockErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT)
	}))
	require.NoError(t, sockErr)
	assert.Equal(t, 5000, got)
}

func TestNetDialerSetsTCPKeepAlive(t *testing.T) {
	srv := newFakeRedis(t, nil)
	d := newNetDialer(time.Second, clientOptions{tcpKeepAlive: 7 * time.Second}, false, nil)

	conn, err := d.DialContext(context.Background(), "tcp", srv.addr())
	require.NoError(t, err)
	defer conn.Close()

	raw, err := conn.(*net.TCPConn).SyscallConn()
	require.NoError(t, err)
	var enabled, idle, interval int
	require.NoError(t, raw.Control(func(fd uintptr) {
		enabled, _ = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_KEEPALIVE)
		idle, _ = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPIDLE)
		interval, _ = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_KEEPINTVL)
	}))
	assert.Equal(t, 1, enabled)
	assert.Equal(t, 7, idle)
	assert.Equal(t, 7, interval)
}
