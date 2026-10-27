package redis

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type activeCutoverCall struct {
	Sequence             int       `json:"sequence"`
	Key                  string    `json:"key"`
	StartedAt            time.Time `json:"started_at"`
	CompletedAt          time.Time `json:"completed_at"`
	SinceCutoverMS       float64   `json:"since_cutover_ms"`
	ElapsedMS            float64   `json:"elapsed_ms"`
	Error                string    `json:"error,omitempty"`
	Value                uint64    `json:"value"`
	MutationConnectionID []int     `json:"mutation_connection_ids"`
}

// TestTLSClusterPoolTenActiveCutoverRecoveryBound sends fresh mutations while
// stale sockets are being retired. Waiting for all PINGs before sending traffic
// would conceal how long callers encounter the rest of a closed generation.
func TestTLSClusterPoolTenActiveCutoverRecoveryBound(t *testing.T) {
	const calls = 9
	server, tlsConfig := newRecoveryRedis(t)
	client := recoveryClient(t, server, tlsConfig, 0, 0)
	warmIDs := warmRecoveryPool(t, server, client)
	warmBefore := server.snapshot()
	closedIDs := server.closeGeneration(t)
	require.Equal(t, warmIDs, closedIDs)
	cutover := time.Now()
	receipts := make([]activeCutoverCall, 0, calls)
	var firstFailure, firstSuccess time.Time
	var errorsAfterRecovery, failures int
	for i := 0; i < calls; i++ {
		if delay := time.Until(cutover.Add(time.Duration(i) * time.Second)); delay > 0 {
			time.Sleep(delay)
		}
		key := fmt.Sprintf("active-cutover-%d", i)
		started := time.Now()
		value, err := recoveryIncrement(client, key)
		completed := time.Now()
		receipt := activeCutoverCall{
			Sequence: i + 1, Key: key, StartedAt: started.UTC(), CompletedAt: completed.UTC(),
			SinceCutoverMS: float64(started.Sub(cutover)) / float64(time.Millisecond),
			ElapsedMS:      float64(completed.Sub(started)) / float64(time.Millisecond), Value: value,
			MutationConnectionID: mutationIDs(server.snapshot(), key),
		}
		if err != nil {
			receipt.Error = err.Error()
			failures++
			if firstFailure.IsZero() {
				firstFailure = completed
			}
			if !firstSuccess.IsZero() {
				errorsAfterRecovery++
			}
		} else if !firstFailure.IsZero() && firstSuccess.IsZero() {
			firstSuccess = completed
		}
		receipts = append(receipts, receipt)
		encoded, encodeErr := json.Marshal(receipt)
		require.NoError(t, encodeErr)
		t.Logf("ACTIVE_CUTOVER_CALL %s", encoded)
	}
	// Collect the complete finite transcript before asserting recovery. Every
	// call uses a distinct key and invokes PipeDo exactly once; a failed call
	// must never be replayed as a hidden increment on a replacement connection.
	final := server.snapshot()
	for _, receipt := range receipts {
		mutations := map[string]int{}
		for _, command := range final.commands {
			if command.key == receipt.Key {
				mutations[command.command]++
			}
		}
		if receipt.Error != "" {
			require.Empty(t, mutations, "closed idle socket's failed mutation must not replay")
			require.Zero(t, final.values[receipt.Key])
		} else {
			require.Equal(t, uint64(1), receipt.Value)
			requireMutationOnce(t, final, receipt.Key)
		}
	}
	for key, value := range warmBefore.values {
		require.Equal(t, value, final.values[key], "cutover must preserve existing quota")
		require.Equal(t, warmBefore.ttls[key], final.ttls[key])
	}
	var oldActive, replacementIDs []int
	server.mu.Lock()
	for id, peer := range server.peers {
		if peer.generation == 1 && peer.active {
			oldActive = append(oldActive, id)
		}
		if peer.generation == 2 && peer.active {
			replacementIDs = append(replacementIDs, id)
		}
	}
	server.mu.Unlock()
	sort.Ints(oldActive)
	sort.Ints(replacementIDs)
	recovery := firstSuccess.Sub(firstFailure)
	summary, err := json.Marshal(map[string]interface{}{
		"scheduled_calls": calls, "actual_calls": len(receipts), "failed_calls": failures,
		"application_retries": 0, "concurrency": 1, "rate_rps": 1,
		"closed_generation_ids": closedIDs, "old_active_ids": oldActive,
		"replacement_active_ids": replacementIDs, "client_connections_created": client.stats.connectionTotal.Value(),
		"client_connections_retired": client.stats.connectionClose.Value(), "client_connections_active": client.stats.connectionActive.Value(),
		"first_failed_completion": firstFailure.UTC(), "first_fresh_success_completion": firstSuccess.UTC(),
		"recovery_ms": float64(recovery) / float64(time.Millisecond), "errors_after_recovery": errorsAfterRecovery,
		"mutations_not_replayed": true, "warm_quotas_preserved": true,
	})
	require.NoError(t, err)
	t.Logf("ACTIVE_CUTOVER_SUMMARY %s", summary)
	require.Empty(t, oldActive)
	require.Len(t, replacementIDs, recoveryPoolSize)
	require.Equal(t, uint64(recoveryPoolSize), client.stats.connectionClose.Value())
	require.Equal(t, uint64(2*recoveryPoolSize), client.stats.connectionTotal.Value())
	require.False(t, firstFailure.IsZero(), "fault must reach a live request")
	require.False(t, firstSuccess.IsZero(), "must observe a fresh successful request after the failure")
	require.Zero(t, errorsAfterRecovery, "no further errors are accepted after recovery")
	require.LessOrEqual(t, recovery, 2*time.Second, "default-cadence active traffic must recover within two seconds of the first failed completion")
}
