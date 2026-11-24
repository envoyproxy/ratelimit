package redis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/envoyproxy/ratelimit/src/settings"
)

type stubGcpTokenSource struct {
	err   error
	block chan struct{}
	calls atomic.Int32
}

func (s *stubGcpTokenSource) Token() (*oauth2.Token, error) {
	s.calls.Add(1)
	if s.block != nil {
		<-s.block
	}
	if s.err != nil {
		return nil, s.err
	}
	return &oauth2.Token{AccessToken: "access-token", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestGcpIAMReportsTokenRetrievalFailure(t *testing.T) {
	provider := &gcpIAMCredentialProvider{tokens: &stubGcpTokenSource{err: errors.New("metadata server unreachable")}}

	_, _, err := provider.Credentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metadata server unreachable")
}

func TestGcpIAMDialsGiveUpAtTheirDeadlineAndShareOneFetch(t *testing.T) {
	tokens := &stubGcpTokenSource{block: make(chan struct{})}
	t.Cleanup(func() { close(tokens.block) })
	provider := &gcpIAMCredentialProvider{tokens: tokens}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			_, _, err := provider.Credentials(ctx)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), tokens.calls.Load(), "dials that overlap a fetch must wait on it, not start their own")
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

func TestCredentialProviderFromSettingsFailsWithoutGoogleCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "testdata/does-not-exist.json")

	assert.Panics(t, func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisGcpIamAuth: true}, false)
	})
}

func TestGcpIAMFetchesTheTokenFromTheMetadataServer(t *testing.T) {
	var (
		mu  sync.Mutex
		got *http.Request
	)
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"metadata-token","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(metadata.Close)

	// Skip the local credential files so the lookup falls through to the
	// metadata server, which GCE_METADATA_HOST points at the fake one.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(metadata.URL, "http://"))

	provider := newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisGcpIamAuth: true}, false)
	user, pass, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Empty(t, user)
	assert.Equal(t, "metadata-token", pass)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, got)
	assert.Equal(t, "/computeMetadata/v1/instance/service-accounts/default/token", got.URL.Path)
	assert.Equal(t, "Google", got.Header.Get("Metadata-Flavor"))
	assert.Equal(t, gcpIAMTokenScope, got.URL.Query().Get("scopes"))
}
