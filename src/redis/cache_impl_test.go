package redis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIOTimeoutFromSettings(t *testing.T) {
	// Unset: REDIS_TIMEOUT bounds I/O too.
	assert.Equal(t, 3*time.Second, ioTimeout(nil, 3*time.Second))

	override := 500 * time.Millisecond
	assert.Equal(t, override, ioTimeout(&override, 3*time.Second))

	// Explicitly 0 disables it, rather than falling back to REDIS_TIMEOUT.
	disabled := time.Duration(0)
	assert.Equal(t, time.Duration(0), ioTimeout(&disabled, 3*time.Second))
}
