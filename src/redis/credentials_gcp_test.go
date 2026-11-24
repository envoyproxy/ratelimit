package redis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mediocregopher/radix/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/envoyproxy/ratelimit/src/settings"
)

type stubGcpTokenSource struct {
	err error
}

func (s *stubGcpTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &oauth2.Token{AccessToken: "access-token", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestGcpIAMAuthenticatesWithTheAccessTokenAsThePassword(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireAuth("access-token")

	provider := &gcpIAMCredentialProvider{tokens: &stubGcpTokenSource{}}

	conn, err := wrapDialerCredentialProvider(radix.Dialer{}, provider).Dial(context.Background(), "tcp", srv.Addr())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestGcpIAMReportsTokenRetrievalFailure(t *testing.T) {
	provider := &gcpIAMCredentialProvider{tokens: &stubGcpTokenSource{err: errors.New("metadata server unreachable")}}

	_, _, err := provider.Credentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata server unreachable")
}

func TestCredentialProviderFromSettingsRejectsGcpIamWithAnotherSource(t *testing.T) {
	assert.PanicsWithError(t, "REDIS_GCP_IAM_AUTH cannot be combined with REDIS_AWS_IAM_AUTH, REDIS_AUTH or REDIS_AUTH_FILE", func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{
			RedisGcpIamAuth: true,
			RedisAuthFile:   "/run/secrets/redis-auth",
		}, false)
	})

	assert.PanicsWithError(t, "REDIS_PERSECOND_GCP_IAM_AUTH cannot be combined with REDIS_PERSECOND_AWS_IAM_AUTH, REDIS_PERSECOND_AUTH or REDIS_PERSECOND_AUTH_FILE", func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{
			RedisPerSecondGcpIamAuth: true,
			RedisPerSecondAwsIamAuth: true,
		}, true)
	})
}

func TestCredentialProviderFromSettingsUsesGoogleDefaultCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "testdata/gcp-authorized-user.json")

	provider := newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisGcpIamAuth: true}, false)
	assert.IsType(t, &gcpIAMCredentialProvider{}, provider)

	assert.Nil(t, newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisPerSecondGcpIamAuth: true}, false),
		"the per-second setting must not enable GCP IAM on the other pool")
}

func TestCredentialProviderFromSettingsFailsWithoutGoogleCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "testdata/does-not-exist.json")

	assert.Panics(t, func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisGcpIamAuth: true}, false)
	})
}

func TestGcpIAMFetchesTheTokenFromTheMetadataServer(t *testing.T) {
	var got *http.Request
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"metadata-token","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(metadata.Close)

	// Skip the local credential files so the lookup falls through to the
	// metadata server, which GCE_METADATA_HOST points at the fake one.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	provider := newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisGcpIamAuth: true}, false)
	user, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Empty(t, user)
	assert.Equal(t, "metadata-token", pass)

	require.NotNil(t, got)
	assert.Equal(t, "/computeMetadata/v1/instance/service-accounts/default/token", got.URL.Path)
	assert.Equal(t, "Google", got.Header.Get("Metadata-Flavor"))
	assert.Equal(t, gcpIAMTokenScope, got.URL.Query().Get("scopes"))
}
