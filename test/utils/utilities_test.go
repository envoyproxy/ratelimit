package utils_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/utils"
)

func TestMaskCredentialsInUrl(t *testing.T) {
	url := "redis:6379"
	assert.Equal(t, url, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:bar@redis:6379"
	expected := "redis://*****@redis:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}

func TestMaskCredentialsInUrlCluster(t *testing.T) {
	url := "redis1:6379,redis2:6379"
	assert.Equal(t, url, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:bar@redis1:6379,redis://foo:bar@redis2:6379"
	expected := "redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))

	url = "redis://foo:b@r@redis1:6379,redis://foo:b@r@redis2:6379"
	expected = "redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}

func TestMaskCredentialsInUrlSentinel(t *testing.T) {
	url := "foobar,redis://foo:bar@redis1:6379,redis://foo:bar@redis2:6379"
	expected := "foobar,redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))

	url = "foob@r,redis://foo:b@r@redis1:6379,redis://foo:b@r@redis2:6379"
	expected = "foob@r,redis://*****@redis1:6379,redis://*****@redis2:6379"
	assert.Equal(t, expected, utils.MaskCredentialsInUrl(url))
}
