package settings

import (
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
