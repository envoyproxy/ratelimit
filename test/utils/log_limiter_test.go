package utils_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/utils"
)

func TestLogLimiter(t *testing.T) {
	l := utils.NewLogLimiter(50 * time.Millisecond)

	ok, suppressed := l.Allow()
	assert.True(t, ok)
	assert.Equal(t, uint64(0), suppressed)

	for i := 0; i < 3; i++ {
		ok, _ = l.Allow()
		assert.False(t, ok)
	}

	time.Sleep(60 * time.Millisecond)
	ok, suppressed = l.Allow()
	assert.True(t, ok)
	assert.Equal(t, uint64(3), suppressed)
}
