package redis

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/mediocregopher/radix/v4/trace"
	"github.com/stretchr/testify/require"
)

const recoveryPoolSize = 10

// recoveryRedis is a verified-TLS, authenticated, single-node cluster fixture.
// It retains values across a generation change and records the physical socket
// receiving each command. Closing every peer leaves ten stale idle client
// sockets; accepting new sockets alone does not prove that the pool retired them.
type recoveryRedis struct {
	listener   net.Listener
	acceptDone chan struct{}
	workers    sync.WaitGroup
	mu         sync.Mutex
	changed    chan struct{}
	closing    bool
	generation int
	nextID     int
	peers      map[int]*recoveryPeer
	commands   []recoveryCommand
	values     map[string]int
	ttls       map[string]int
	dropKey    string
	errors     []error
}

type recoveryPeer struct {
	conn                  net.Conn
	id, generation        int
	active, authenticated bool
	pings                 int
	checked               int
	topologyReplies       int
}

type recoveryCommand struct {
	connectionID, generation int
	command, key             string
}

type recoverySnapshot struct {
	generation                             int
	topologyReplies                        int
	active, authenticated, pinged, checked []int
	commands                               []recoveryCommand
	values, ttls                           map[string]int
	errors                                 []error
}

func newRecoveryRedis(t *testing.T) (*recoveryRedis, *tls.Config) {
	t.Helper()
	serverTLS, clientTLS := selfSignedTLS(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	require.NoError(t, err)
	s := &recoveryRedis{
		listener: listener, acceptDone: make(chan struct{}), changed: make(chan struct{}), generation: 1,
		peers: make(map[int]*recoveryPeer), values: make(map[string]int), ttls: make(map[string]int),
	}
	go s.accept()
	t.Cleanup(func() {
		_ = s.listener.Close()
		s.mu.Lock()
		s.closing = true
		var conns []net.Conn
		for _, peer := range s.peers {
			conns = append(conns, peer.conn)
		}
		s.mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		select {
		case <-s.acceptDone:
		case <-time.After(time.Second):
			t.Fatal("recovery fixture accept did not join")
		}
		joined := make(chan struct{})
		go func() { s.workers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Fatal("recovery fixture peers did not join")
		}
		require.Empty(t, s.snapshot().errors)
	})
	return s, clientTLS
}

func (s *recoveryRedis) notifyLocked() { close(s.changed); s.changed = make(chan struct{}) }

func (s *recoveryRedis) accept() {
	defer close(s.acceptDone)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.nextID++
		peer := &recoveryPeer{conn: conn, id: s.nextID, generation: s.generation, active: true}
		s.peers[peer.id] = peer
		s.workers.Add(1)
		s.notifyLocked()
		s.mu.Unlock()
		go func() {
			defer s.workers.Done()
			defer conn.Close()
			defer func() { s.mu.Lock(); peer.active = false; s.notifyLocked(); s.mu.Unlock() }()
			s.serve(peer)
		}()
	}
}

func (s *recoveryRedis) serve(peer *recoveryPeer) {
	reader := bufio.NewReader(peer.conn)
	for {
		args, err := readEOFCommand(reader)
		if err != nil {
			return
		}
		command, key := strings.ToUpper(args[0]), ""
		if len(args) > 1 {
			key = args[1]
		}
		s.mu.Lock()
		reply := "+OK\r\n"
		s.commands = append(s.commands, recoveryCommand{peer.id, peer.generation, command, key})
		if command == "AUTH" {
			if len(args) == 2 && args[1] == "recovery-fixture-password" {
				peer.authenticated = true
			} else {
				reply = "-ERR invalid password\r\n"
			}
		} else if !peer.authenticated {
			s.errors = append(s.errors, errors.New("command arrived before authentication"))
			reply = "-NOAUTH authentication required\r\n"
		} else {
			switch command {
			case "PING":
				reply = "+PONG\r\n"
			case "CLUSTER":
				if len(args) != 2 || args[1] != "SLOTS" {
					s.errors = append(s.errors, fmt.Errorf("unexpected cluster command %q", args))
					break
				}
				host, port, _ := net.SplitHostPort(s.listener.Addr().String())
				reply = fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*3\r\n$%d\r\n%s\r\n:%s\r\n$8\r\nrecovery\r\n", len(host), host, port)
			case "INCRBY", "EXPIRE":
				if len(args) != 3 {
					s.errors = append(s.errors, fmt.Errorf("unexpected mutation %q", args))
					break
				}
				n, err := strconv.Atoi(args[2])
				if err != nil {
					s.errors = append(s.errors, err)
					break
				}
				if command == "INCRBY" {
					s.values[key] += n
					reply = fmt.Sprintf(":%d\r\n", s.values[key])
				} else {
					s.ttls[key] = n
					reply = ":1\r\n"
				}
			default:
				s.errors = append(s.errors, fmt.Errorf("unexpected command %q", args))
			}
		}
		drop := key != "" && key == s.dropKey && (command == "INCRBY" || command == "EXPIRE")
		s.notifyLocked()
		s.mu.Unlock()
		if drop {
			// Both mutations commit before closing, with neither reply delivered.
			if command == "EXPIRE" {
				return
			}
			continue
		}
		if _, err := io.WriteString(peer.conn, reply); err != nil {
			return
		}
		if command == "PING" || command == "CLUSTER" {
			s.mu.Lock()
			peer.checked++
			if command == "PING" {
				peer.pings++
			} else {
				peer.topologyReplies++
			}
			s.notifyLocked()
			s.mu.Unlock()
		}
	}
}

func (s *recoveryRedis) snapshotLocked() recoverySnapshot {
	v := recoverySnapshot{generation: s.generation, commands: append([]recoveryCommand(nil), s.commands...), values: make(map[string]int), ttls: make(map[string]int), errors: append([]error(nil), s.errors...)}
	for id, peer := range s.peers {
		v.topologyReplies += peer.topologyReplies
		if !peer.active || peer.generation != s.generation {
			continue
		}
		v.active = append(v.active, id)
		if peer.authenticated {
			v.authenticated = append(v.authenticated, id)
		}
		if peer.pings > 0 {
			v.pinged = append(v.pinged, id)
		}
		if peer.checked > 0 {
			v.checked = append(v.checked, id)
		}
	}
	for key, value := range s.values {
		v.values[key] = value
	}
	for key, value := range s.ttls {
		v.ttls[key] = value
	}
	sort.Ints(v.active)
	sort.Ints(v.authenticated)
	sort.Ints(v.pinged)
	sort.Ints(v.checked)
	return v
}

func (s *recoveryRedis) snapshot() recoverySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// wait observes fixture events, rather than sleeping and assuming a background
// health check or asynchronous pool initialization has completed.
func (s *recoveryRedis) wait(t *testing.T, timeout time.Duration, ready func(recoverySnapshot) bool) recoverySnapshot {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		v, changed := s.snapshotLocked(), s.changed
		s.mu.Unlock()
		if ready(v) {
			return v
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("fixture barrier expired: generation=%d active=%v auth=%v pinged=%v", v.generation, v.active, v.authenticated, v.pinged)
		}
	}
}

func (s *recoveryRedis) closeGeneration(t *testing.T) []int {
	t.Helper()
	s.mu.Lock()
	oldGeneration := s.generation
	var ids []int
	var peers []net.Conn
	for id, peer := range s.peers {
		if peer.active && peer.generation == oldGeneration {
			ids = append(ids, id)
			peers = append(peers, peer.conn)
		}
	}
	s.generation++
	s.notifyLocked()
	s.mu.Unlock()
	for _, conn := range peers {
		require.NoError(t, conn.Close())
	}
	// Observe every old serving goroutine leave; a close call alone is not a
	// server-side barrier proving that all old peers have disappeared.
	s.wait(t, time.Second, func(recoverySnapshot) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, peer := range s.peers {
			if peer.active && peer.generation == oldGeneration {
				return false
			}
		}
		return true
	})
	sort.Ints(ids)
	return ids
}

func recoveryClient(t *testing.T, server *recoveryRedis, clientTLS *tls.Config, interval, syncEvery time.Duration) *clientImpl {
	t.Helper()
	initialized := make(chan struct{}, 1)
	poolStats := newPoolStats(stats.NewStore(stats.NewNullSink(), false))
	poolTrace := poolTrace(&poolStats, false, nil)
	poolTrace.InitCompleted = func(trace.PoolInitCompleted) { initialized <- struct{}{} }
	dialer := createDialer(time.Second, clientOptions{}, true, clientTLS, "recovery-fixture-password", "")
	dialer.WriteFlushInterval = 150 * time.Microsecond
	config := exclusivePoolConfig(radix.PoolConfig{Size: recoveryPoolSize, PingInterval: interval, Dialer: dialer, Trace: poolTrace}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// A negative sync interval isolates health. Zero retains the production
	// default five-second topology refresh in the interaction subcase.
	cluster, err := (radix.ClusterConfig{PoolConfig: config, SyncEvery: syncEvery}).New(ctx, []string{server.listener.Addr().String()})
	require.NoError(t, err)
	client := &clientImpl{client: cluster, stats: poolStats, isCluster: true, clusterPipelineParallelism: 1}
	t.Cleanup(func() { require.NoError(t, client.Close()); require.Zero(t, client.stats.connectionActive.Value()) })
	select {
	case <-initialized:
	case <-time.After(2 * time.Second):
		t.Fatal("ten-slot pool initialization did not complete")
	}
	server.wait(t, time.Second, func(v recoverySnapshot) bool { return len(v.authenticated) == recoveryPoolSize })
	return client
}

func recoveryIncrement(client *clientImpl, key string) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result uint64
	pipeline := client.PipeAppend(nil, &result, "INCRBY", key, 1)
	pipeline = client.PipeAppend(pipeline, nil, "EXPIRE", key, 60)
	err := client.PipeDo(ctx, pipeline)
	return result, err
}

func mutationIDs(v recoverySnapshot, prefix string) []int {
	ids := map[int]bool{}
	for _, c := range v.commands {
		if c.command == "INCRBY" && strings.HasPrefix(c.key, prefix) {
			ids[c.connectionID] = true
		}
	}
	result := make([]int, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}

func warmRecoveryPool(t *testing.T, server *recoveryRedis, client *clientImpl) []int {
	t.Helper()
	for i := 0; i < 100; i++ {
		value, err := recoveryIncrement(client, fmt.Sprintf("warm-%d", i))
		require.NoError(t, err)
		require.Equal(t, uint64(1), value)
		if ids := mutationIDs(server.snapshot(), "warm-"); len(ids) == recoveryPoolSize {
			return ids
		}
	}
	t.Fatal("warm requests did not visit every physical pool connection")
	return nil
}

func requireMutationOnce(t *testing.T, v recoverySnapshot, key string) {
	t.Helper()
	counts := map[string]int{}
	for _, command := range v.commands {
		if command.key == key {
			counts[command.command]++
		}
	}
	require.Equal(t, map[string]int{"INCRBY": 1, "EXPIRE": 1}, counts, "pipeline was replayed or partially dispatched for %s", key)
	require.Equal(t, 1, v.values[key])
	require.Equal(t, 60, v.ttls[key])
}

func TestTLSClusterPoolTenDisabledHealthRetainsStaleIdleSockets(t *testing.T) {
	server, tlsConfig := newRecoveryRedis(t)
	client := recoveryClient(t, server, tlsConfig, -1, -1)
	warmIDs := warmRecoveryPool(t, server, client)
	require.Equal(t, warmIDs, server.closeGeneration(t))
	failures := 0
	for i := 0; i < recoveryPoolSize; i++ {
		key := fmt.Sprintf("stale-%d", i)
		_, err := recoveryIncrement(client, key)
		if err != nil {
			failures++
		}
		// A peer that closed before dispatch must not receive a mutation, and
		// the client must not replay that failed increment on a new socket.
		require.Zero(t, server.snapshot().values[key])
	}
	require.Equal(t, recoveryPoolSize, failures)
	require.Equal(t, uint64(recoveryPoolSize), client.stats.connectionClose.Value())
	t.Logf("health disabled: stale physical IDs=%v, ten distinct failed calls, zero mutation replays", warmIDs)
}

func TestTLSClusterPoolTenOwnedHealthRetiresEveryStaleSocket(t *testing.T) {
	for _, tt := range []struct {
		name                string
		interval, syncEvery time.Duration
	}{
		{"explicit_health_isolated", 20 * time.Millisecond, -1},
		{"default_health_isolated", 0, -1},
		{"default_health_and_topology", 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, tlsConfig := newRecoveryRedis(t)
			client := recoveryClient(t, server, tlsConfig, tt.interval, tt.syncEvery)
			warmIDs := warmRecoveryPool(t, server, client)
			warmBefore := server.snapshot()
			require.Equal(t, warmIDs, server.closeGeneration(t))
			// No mutation is used as a recovery probe. With topology disabled,
			// every new peer must answer PING. Default topology refresh can
			// consume a FIFO slot instead, so count its successful read-only
			// CLUSTER SLOTS response as a health check for that physical peer.
			ready := server.wait(t, 12*time.Second, func(v recoverySnapshot) bool {
				if len(v.authenticated) != recoveryPoolSize || len(v.checked) != recoveryPoolSize {
					return false
				}
				if tt.syncEvery < 0 {
					return len(v.pinged) == recoveryPoolSize
				}
				return v.topologyReplies >= 2 && len(v.pinged) > 0
			})
			require.Equal(t, uint64(recoveryPoolSize), client.stats.connectionClose.Value(), "every stale socket must be retired before accepting the barrier")
			require.Equal(t, uint64(2*recoveryPoolSize), client.stats.connectionTotal.Value())
			require.NotEqual(t, warmIDs, ready.checked)
			for key, value := range warmBefore.values {
				require.Equal(t, value, ready.values[key], "restart reset or duplicated a warm quota")
				require.Equal(t, warmBefore.ttls[key], ready.ttls[key], "restart lost a warm quota expiry")
			}
			if tt.syncEvery == 0 {
				require.GreaterOrEqual(t, ready.topologyReplies, 2, "default topology refresh must complete a reply during recovery")
			}
			for i := 0; i < 2*recoveryPoolSize; i++ {
				key := fmt.Sprintf("fresh-%d", i)
				value, err := recoveryIncrement(client, key)
				require.NoError(t, err)
				require.Equal(t, uint64(1), value)
				requireMutationOnce(t, server.snapshot(), key)
			}
			t.Logf("health interval=%s topology=%s: retired IDs=%v replacement checked IDs=%v PING IDs=%v topology replies=%d; twenty fresh pipelines succeeded once each", tt.interval, tt.syncEvery, warmIDs, ready.checked, ready.pinged, ready.topologyReplies)
		})
	}
}

func TestTLSClusterPoolTenHealthDoesNotReplayCommittedReplyLoss(t *testing.T) {
	server, tlsConfig := newRecoveryRedis(t)
	client := recoveryClient(t, server, tlsConfig, 20*time.Millisecond, -1)
	warmRecoveryPool(t, server, client)
	server.mu.Lock()
	server.dropKey = "committed-reply-lost"
	server.mu.Unlock()
	_, err := recoveryIncrement(client, "committed-reply-lost")
	require.ErrorIs(t, err, io.EOF)
	requireMutationOnce(t, server.snapshot(), "committed-reply-lost")
	value, err := recoveryIncrement(client, "distinct-after-loss")
	require.NoError(t, err)
	require.Equal(t, uint64(1), value)
	requireMutationOnce(t, server.snapshot(), "distinct-after-loss")
	requireMutationOnce(t, server.snapshot(), "committed-reply-lost")
	t.Log("committed reply loss remains EOF; original increment and expiry each executed once; next distinct request succeeded")
}
