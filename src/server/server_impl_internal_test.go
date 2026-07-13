package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaxConcurrentStreamsOptions_Disabled(t *testing.T) {
	opts := maxConcurrentStreamsOptions(0)
	assert.Empty(t, opts)
}

func TestMaxConcurrentStreamsOptions_Enabled(t *testing.T) {
	opts := maxConcurrentStreamsOptions(100)
	assert.Len(t, opts, 1)
}
