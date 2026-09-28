package redis

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestRedisAuthFileConnectsWithUsernameAndPasswordWhitespace(t *testing.T) {
	server := miniredis.RunT(t)
	server.RequireUserAuth("alice", " leading:trailing ")
	file := filepath.Join(t.TempDir(), "redis-auth")
	require.NoError(t, os.WriteFile(file, []byte("alice: leading:trailing \r\n"), 0o600))

	auth, err := redisAuthFromFile("", file, "REDIS_AUTH", "REDIS_AUTH_FILE")
	require.NoError(t, err)
	dialer := createDialer(time.Second, false, nil, auth, server.Addr())
	conn, err := dialer.Dial(context.Background(), "tcp", server.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	var response string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&response, "PING")))
	assert.Equal(t, "PONG", response)

	// The dialer keeps the startup credential even if the mounted file rotates.
	require.NoError(t, os.WriteFile(file, []byte("alice:rotated\n"), 0o600))
	secondConn, err := dialer.Dial(context.Background(), "tcp", server.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, secondConn.Close()) })
	rotatedAuth, err := redisAuthFromFile("", file, "REDIS_AUTH", "REDIS_AUTH_FILE")
	require.NoError(t, err)
	assert.Equal(t, "alice:rotated", rotatedAuth)
	assert.Equal(t, " leading:trailing ", dialer.AuthPass)
}

func TestRedisAuthFileAuthenticatesOverTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	parsedCertificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(parsedCertificate)

	server := miniredis.NewMiniRedis()
	require.NoError(t, server.StartTLS(&tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}))
	t.Cleanup(server.Close)
	server.RequireUserAuth("alice", " password with spaces ")

	file := filepath.Join(t.TempDir(), "redis-auth")
	require.NoError(t, os.WriteFile(file, []byte("alice: password with spaces \n"), 0o600))

	auth, err := redisAuthFromFile("", file, "REDIS_AUTH", "REDIS_AUTH_FILE")
	require.NoError(t, err)
	tlsConfig := &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	dialer := createDialer(time.Second, true, tlsConfig, auth, server.Addr())

	assert.Equal(t, "alice", dialer.AuthUser)
	assert.Equal(t, " password with spaces ", dialer.AuthPass)
	tlsDialer, ok := dialer.NetDialer.(*tls.Dialer)
	require.True(t, ok)
	assert.Same(t, tlsConfig, tlsDialer.Config)
	conn, err := dialer.Dial(context.Background(), "tcp", server.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	var response string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&response, "PING")))
	assert.Equal(t, "PONG", response)
}
