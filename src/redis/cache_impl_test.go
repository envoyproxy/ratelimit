package redis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedisAuthFromFileSupportsUsernamePassword(t *testing.T) {
	authFile := filepath.Join(t.TempDir(), "redis-auth")
	assert.NoError(t, os.WriteFile(authFile, []byte(" username:password \r\n\n"), 0o600))

	auth, err := redisAuthFromFile("", authFile, "REDIS_AUTH", "REDIS_AUTH_FILE")

	assert.NoError(t, err)
	assert.Equal(t, " username:password ", auth)
}

func TestRedisAuthFromFilePreservesInlineAuth(t *testing.T) {
	auth, err := redisAuthFromFile("username:password", "", "REDIS_AUTH", "REDIS_AUTH_FILE")

	assert.NoError(t, err)
	assert.Equal(t, "username:password", auth)
}

func TestRedisAuthFromFileRejectsConflictingSources(t *testing.T) {
	inlineAuth := "credential-that-must-not-leak"
	for _, test := range []struct {
		name      string
		inlineEnv string
		fileEnv   string
	}{
		{name: "main", inlineEnv: "REDIS_AUTH", fileEnv: "REDIS_AUTH_FILE"},
		{name: "per-second", inlineEnv: "REDIS_PERSECOND_AUTH", fileEnv: "REDIS_PERSECOND_AUTH_FILE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, err := redisAuthFromFile(inlineAuth, "unused", test.inlineEnv, test.fileEnv)

			assert.Empty(t, auth)
			assert.EqualError(t, err, test.inlineEnv+" and "+test.fileEnv+" cannot both be set")
			assert.NotContains(t, err.Error(), inlineAuth)
		})
	}
}

func TestRedisAuthFromFileMustBeReadable(t *testing.T) {
	auth, err := redisAuthFromFile("", filepath.Join(t.TempDir(), "missing"), "REDIS_AUTH", "REDIS_AUTH_FILE")

	assert.Empty(t, auth)
	assert.ErrorContains(t, err, "read REDIS_AUTH_FILE")
}

func TestRedisAuthFromFileMustNotBeEmpty(t *testing.T) {
	authFile := filepath.Join(t.TempDir(), "redis-auth")
	assert.NoError(t, os.WriteFile(authFile, []byte("\r\n"), 0o600))

	auth, err := redisAuthFromFile("", authFile, "REDIS_AUTH", "REDIS_AUTH_FILE")

	assert.Empty(t, auth)
	assert.EqualError(t, err, "REDIS_AUTH_FILE must not reference an empty file")
}
