package settings

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestLimitsSettings(t *testing.T) {
	for _, tc := range []struct {
		name, max, timeout string
		wantMax            int
		wantTimeout        time.Duration
		invalid            bool
	}{
		{name: "disabled", max: "0", timeout: "0"},
		{name: "configured", max: "32", timeout: "250ms", wantMax: 32, wantTimeout: 250 * time.Millisecond},
		{name: "negative concurrency", max: "-1", timeout: "0", invalid: true},
		{name: "negative timeout", max: "0", timeout: "-1s", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAX_CONCURRENT_REQUESTS", tc.max)
			t.Setenv("REQUEST_TIMEOUT", tc.timeout)
			if tc.invalid {
				require.Panics(t, func() { NewSettings() })
				return
			}
			s := NewSettings()
			require.Equal(t, tc.wantMax, s.MaxConcurrentRequests)
			require.Equal(t, tc.wantTimeout, s.RequestTimeout)
		})
	}
}

func TestGrpcMaxConcurrentStreamsSettings(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("GRPC_MAX_CONCURRENT_STREAMS", "")
		require.NoError(t, os.Unsetenv("GRPC_MAX_CONCURRENT_STREAMS"))
		s := NewSettings()
		require.Zero(t, s.GrpcMaxConcurrentStreams)
	})
	for _, tc := range []struct {
		name, value string
		want        uint32
		invalid     bool
	}{
		{name: "disabled", value: "0"},
		{name: "configured", value: "16", want: 16},
		{name: "largest valid", value: "4294967294", want: 4294967294},
		{name: "grpc unlimited sentinel", value: "4294967295", invalid: true},
		{name: "negative", value: "-1", invalid: true},
		{name: "overflow", value: "4294967296", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GRPC_MAX_CONCURRENT_STREAMS", tc.value)
			if tc.invalid {
				require.Panics(t, func() { NewSettings() })
				return
			}
			require.Equal(t, tc.want, NewSettings().GrpcMaxConcurrentStreams)
		})
	}
}
