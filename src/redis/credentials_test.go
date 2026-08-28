package redis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	stats "github.com/lyft/gostats"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/settings"
)

type stubCredentialProvider struct {
	user, pass string
	calls      int
	err        error
}

func (p *stubCredentialProvider) Credentials(_ context.Context) (string, string, error) {
	p.calls++
	if p.err != nil {
		return "", "", p.err
	}
	return p.user, p.pass, nil
}

func requireAuthenticatedConn(t *testing.T, dialer radix.Dialer, addr string) radix.Conn {
	t.Helper()

	conn, err := dialer.Dial(context.Background(), "tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var res string
	require.NoError(t, conn.Do(context.Background(), radix.Cmd(&res, "SET", "key", "value")))
	require.Equal(t, "OK", res)

	return conn
}

func TestCredentialProviderResolvedOnEveryDial(t *testing.T) {
	srv := miniredis.RunT(t)
	provider := &stubCredentialProvider{user: "cache-user", pass: "first-password"}
	dialer := wrapDialerCredentialProvider(radix.Dialer{}, provider)

	srv.RequireUserAuth(provider.user, provider.pass)
	requireAuthenticatedConn(t, dialer, srv.Addr())

	srv.RequireUserAuth(provider.user, "second-password")
	provider.pass = "second-password"

	requireAuthenticatedConn(t, dialer, srv.Addr())

	assert.Equal(t, 2, provider.calls)
}

func TestCredentialProviderStaleCredentialsAreRejected(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireUserAuth("cache-user", "current-password")

	dialer := wrapDialerCredentialProvider(radix.Dialer{}, &stubCredentialProvider{
		user: "cache-user",
		pass: "expired-password",
	})

	_, err := dialer.Dial(context.Background(), "tcp", srv.Addr())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONGPASS")
}

func TestCredentialProviderComposesWithCloseOnReadOnly(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireUserAuth("cache-user", "token")

	dialer := wrapDialerCloseOnReadOnly(wrapDialerCredentialProvider(radix.Dialer{}, &stubCredentialProvider{
		user: "cache-user",
		pass: "token",
	}))

	conn := requireAuthenticatedConn(t, dialer, srv.Addr())
	assert.IsType(t, readOnlyClosingConn{}, conn)
}

func TestCredentialProviderRefusesADialerItCannotAuthenticate(t *testing.T) {
	base := wrapDialerCloseOnReadOnly(radix.Dialer{})

	assert.PanicsWithError(t, "the Redis credential provider must wrap the dialer before any CustomConn dialer, otherwise connections are not authenticated", func() {
		wrapDialerCredentialProvider(base, &stubCredentialProvider{})
	})
}

func TestCredentialProviderErrorFailsTheDial(t *testing.T) {
	srv := miniredis.RunT(t)
	provider := &stubCredentialProvider{err: errors.New("credentials unavailable")}

	_, err := wrapDialerCredentialProvider(radix.Dialer{}, provider).Dial(context.Background(), "tcp", srv.Addr())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials unavailable")
}

func TestSplitAuth(t *testing.T) {
	user, pass, hasUser := splitAuth("cache-user:s3cret")
	assert.Equal(t, "cache-user", user)
	assert.Equal(t, "s3cret", pass)
	assert.True(t, hasUser)

	user, pass, hasUser = splitAuth("s3cret")
	assert.Empty(t, user)
	assert.Equal(t, "s3cret", pass)
	assert.False(t, hasUser)

	user, pass, _ = splitAuth("cache-user:s3:cret")
	assert.Equal(t, "cache-user", user)
	assert.Equal(t, "s3:cret", pass)
}

func writeAuthFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "redis-auth")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestFileCredentialProviderReadsUsernameAndPassword(t *testing.T) {
	provider := newFileCredentialProvider(writeAuthFile(t, "cache-user:s3cret\n"))

	user, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cache-user", user)
	assert.Equal(t, "s3cret", pass)
}

func TestFileCredentialProviderReadsPasswordOnlyFile(t *testing.T) {
	provider := newFileCredentialProvider(writeAuthFile(t, "  s3cret\n\n"))

	user, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Empty(t, user)
	assert.Equal(t, "s3cret", pass)
}

func TestFileCredentialProviderRereadsTheFile(t *testing.T) {
	path := writeAuthFile(t, "cache-user:first-password")
	provider := newFileCredentialProvider(path)

	_, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, "first-password", pass)

	require.NoError(t, os.WriteFile(path, []byte("cache-user:second-password"), 0o600))

	_, pass, err = provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "second-password", pass)
}

func TestFileCredentialProviderReportsUnreadableFile(t *testing.T) {
	provider := newFileCredentialProvider(filepath.Join(t.TempDir(), "does-not-exist"))

	_, _, err := provider.Credentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}

func TestFileCredentialProviderRejectsEmptyFile(t *testing.T) {
	provider := newFileCredentialProvider(writeAuthFile(t, "\n"))

	_, _, err := provider.Credentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestCredentialProviderFromSettingsIsNilWhenUnconfigured(t *testing.T) {
	assert.Nil(t, newCredentialProviderFromSettings(settings.Settings{}, false))
	assert.Nil(t, newCredentialProviderFromSettings(settings.Settings{RedisAuth: "s3cret"}, false))
}

func TestCredentialProviderFromSettingsUsesTheCredentialFile(t *testing.T) {
	path := writeAuthFile(t, "cache-user:s3cret")

	provider := newCredentialProviderFromSettings(settings.Settings{RedisAuthFile: path}, false)
	require.NotNil(t, provider)

	user, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cache-user", user)
	assert.Equal(t, "s3cret", pass)
}

func TestCredentialProviderFromSettingsReadsPerSecondSettings(t *testing.T) {
	s := settings.Settings{
		RedisAuthFile:          writeAuthFile(t, "other-pool-password"),
		RedisPerSecondAuthFile: writeAuthFile(t, "per-second-password"),
	}

	provider := newCredentialProviderFromSettings(s, true)
	require.NotNil(t, provider)

	_, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "per-second-password", pass)
}

func TestCredentialProviderFromSettingsRejectsTwoCredentialSources(t *testing.T) {
	s := settings.Settings{RedisAuth: "s3cret", RedisAuthFile: "/run/secrets/redis-auth"}

	assert.PanicsWithError(t, "REDIS_AUTH and REDIS_AUTH_FILE are mutually exclusive", func() {
		newCredentialProviderFromSettings(s, false)
	})

	perSecond := settings.Settings{RedisPerSecondAuth: "s3cret", RedisPerSecondAuthFile: "/run/secrets/redis-auth"}

	assert.PanicsWithError(t, "REDIS_PERSECOND_AUTH and REDIS_PERSECOND_AUTH_FILE are mutually exclusive", func() {
		newCredentialProviderFromSettings(perSecond, true)
	})
}

func newTestClient(t *testing.T, addr string, closeOnReadOnly bool, provider CredentialProvider) Client {
	t.Helper()

	return newClientImpl(context.Background(), stats.NewStore(stats.NewNullSink(), false), false, "", "tcp",
		"single", addr, 1, 0, 0, nil, false, nil, 10*time.Second, "", "", time.Second, 30*time.Second,
		100*time.Millisecond, 1, closeOnReadOnly, provider)
}

func TestNewClientImplAuthenticatesWithCredentialProvider(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireUserAuth("cache-user", "token")
	provider := &stubCredentialProvider{user: "cache-user", pass: "token"}

	var client Client
	require.NotPanics(t, func() { client = newTestClient(t, srv.Addr(), false, provider) })
	require.NoError(t, client.Close())
	assert.Positive(t, provider.calls)
}

func TestNewClientImplAuthenticatesWithCredentialProviderAndCloseOnReadOnly(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireUserAuth("cache-user", "token")

	var client Client
	require.NotPanics(t, func() {
		client = newTestClient(t, srv.Addr(), true, &stubCredentialProvider{user: "cache-user", pass: "token"})
	})
	require.NoError(t, client.Close())
}
