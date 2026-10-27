package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterBootstrapPreservedOnInitialAndReplacementConnections(t *testing.T) {
	for _, mode := range []string{"cluster", "single"} {
		t.Run(mode, func(t *testing.T) {
			server, clientTLS := newRecoveryRedis(t)
			store := stats.NewStore(stats.NewNullSink(), false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := newClientImpl(ctx, store, true, "recovery-fixture-password", "tcp", mode, server.listener.Addr().String(), 2,
				150*time.Microsecond, 0, clientTLS, false, nil, time.Second, "", "", time.Millisecond, 10*time.Millisecond,
				time.Second, 1, false, nil, clientOptions{ioTimeout: time.Second}).(*clientImpl)
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			check := func(v recoverySnapshot) bool {
				if len(v.active) != 2 || len(v.authenticated) != 2 {
					return false
				}
				for _, id := range v.active {
					readonly := 0
					for _, c := range v.commands {
						if c.connectionID == id && c.command == "READONLY" {
							readonly++
						}
					}
					if mode == "cluster" && readonly != 1 || mode == "single" && readonly != 0 {
						return false
					}
				}
				return true
			}
			initial := server.wait(t, time.Second, check)
			require.Len(t, initial.active, 2)
			oldIDs := server.closeGeneration(t)
			require.Len(t, oldIDs, 2)
			// These are fresh, read-only health probes, not retries of mutations. They
			// discover both stale slots so replacement bootstrap can be observed.
			for i := 0; i < 2; i++ {
				_ = client.client.Do(ctx, radix.Cmd(nil, "PING"))
			}
			replaced := server.wait(t, time.Second, check)
			for _, id := range replaced.active {
				assert.NotContains(t, oldIDs, id)
			}
			// Every connection must authenticate before its one cluster bootstrap.
			commands := map[int][]string{}
			for _, c := range replaced.commands {
				commands[c.connectionID] = append(commands[c.connectionID], c.command)
			}
			for _, sequence := range commands {
				require.NotEmpty(t, sequence)
				assert.Equal(t, "AUTH", sequence[0])
				if mode == "cluster" {
					require.GreaterOrEqual(t, len(sequence), 2)
					assert.Equal(t, "READONLY", sequence[1])
				} else {
					assert.NotContains(t, sequence, "READONLY")
				}
			}
		})
	}
}

func TestClusterBootstrapFailureClosesConnection(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	require.NoError(t, peer.SetDeadline(time.Now().Add(time.Second)))
	observed := make(chan error, 1)
	go func() {
		args, err := readEOFCommand(bufio.NewReader(peer))
		if err != nil {
			observed <- err
			return
		}
		if len(args) != 1 || args[0] != "READONLY" {
			observed <- fmt.Errorf("unexpected bootstrap %q", args)
			return
		}
		if _, err = io.WriteString(peer, "-ERR bootstrap rejected\r\n"); err != nil {
			observed <- err
			return
		}
		_, err = peer.Read(make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			observed <- fmt.Errorf("connection was not closed: %v", err)
			return
		}
		observed <- nil
	}()
	config := clusterReadOnlyPoolConfig(radix.PoolConfig{Dialer: radix.Dialer{NetDialer: pipeNetDialer{conn: client}}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := config.Dialer.Dial(ctx, "tcp", "127.0.0.1:6379")
	require.Nil(t, conn)
	require.ErrorContains(t, err, "bootstrap rejected")
	select {
	case err := <-observed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("failed bootstrap did not close the socket")
	}
}

func TestClusterBootstrapPreservesCustomDialerInitialization(t *testing.T) {
	for _, initializesReadOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom_readonly_%t", initializesReadOnly), func(t *testing.T) {
			var commands []string
			dials := 0
			config := clusterReadOnlyPoolConfig(radix.PoolConfig{Dialer: radix.Dialer{CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
				dials++
				conn := radix.NewStubConn(network, addr, func(_ context.Context, args []string) interface{} { commands = append(commands, args[0]); return "OK" })
				if initializesReadOnly {
					if err := conn.Do(ctx, radix.Cmd(nil, "READONLY")); err != nil {
						return nil, err
					}
				}
				return conn, nil
			}}})
			conn, err := config.Dialer.Dial(context.Background(), "tcp", "127.0.0.1:6379")
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.Do(context.Background(), radix.Cmd(nil, "PING")))
			assert.Equal(t, 1, dials)
			expected := []string{"PING"}
			if initializesReadOnly {
				expected = []string{"READONLY", "PING"}
			}
			assert.Equal(t, expected, commands)
		})
	}
}

func TestClusterBootstrapPreservesCustomPool(t *testing.T) {
	var commands []string
	created := 0
	config := clusterReadOnlyPoolConfig(radix.PoolConfig{CustomPool: func(_ context.Context, network, addr string) (radix.Client, error) {
		created++
		return radix.NewStubConn(network, addr, func(_ context.Context, args []string) interface{} { commands = append(commands, args[0]); return "OK" }), nil
	}})
	require.Nil(t, config.Dialer.CustomConn)
	client, err := config.New(context.Background(), "tcp", "127.0.0.1:6379")
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Do(context.Background(), radix.Cmd(nil, "PING")))
	assert.Equal(t, 1, created)
	assert.Equal(t, []string{"PING"}, commands)
}
