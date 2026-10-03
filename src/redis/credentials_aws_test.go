package redis

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/settings"
)

type stubAwsCredentialsProvider struct {
	credentials aws.Credentials
	err         error
	calls       int
}

func (s *stubAwsCredentialsProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	s.calls++
	if s.err != nil {
		return aws.Credentials{}, s.err
	}
	return s.credentials, nil
}

func staticAwsCredentials() *stubAwsCredentialsProvider {
	return &stubAwsCredentialsProvider{credentials: aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "session-token",
	}}
}

func mustParseToken(t *testing.T, token string) *url.URL {
	t.Helper()

	require.False(t, strings.HasPrefix(token, "http://"), "token must not carry a scheme")
	require.False(t, strings.HasPrefix(token, "https://"), "token must not carry a scheme")

	parsed, err := url.Parse("http://" + token)
	require.NoError(t, err)
	return parsed
}

func TestElastiCacheIAMTokenIsAPresignedConnectRequest(t *testing.T) {
	provider := newElastiCacheIAMCredentialProvider(staticAwsCredentials(), "us-east-1", "My-Cache", "cache-user", false)

	user, token, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cache-user", user)

	parsed := mustParseToken(t, token)
	assert.Equal(t, "my-cache", parsed.Host, "the cache name is lowercased at creation, so sign the lowercase name")
	assert.Equal(t, "/", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "connect", query.Get("Action"))
	assert.Equal(t, "cache-user", query.Get("User"))
	assert.Equal(t, "900", query.Get("X-Amz-Expires"))
	assert.Equal(t, "AWS4-HMAC-SHA256", query.Get("X-Amz-Algorithm"))
	assert.Contains(t, query.Get("X-Amz-Credential"), "/us-east-1/elasticache/aws4_request")
	assert.Equal(t, "session-token", query.Get("X-Amz-Security-Token"))
	assert.NotEmpty(t, query.Get("X-Amz-Signature"))
	assert.Empty(t, query.Get("ResourceType"))
}

func TestElastiCacheIAMTokenMarksServerlessCaches(t *testing.T) {
	provider := newElastiCacheIAMCredentialProvider(staticAwsCredentials(), "us-east-1", "my-cache", "cache-user", true)

	_, token, err := provider.Credentials(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "ServerlessCache", mustParseToken(t, token).Query().Get("ResourceType"))
}

func TestElastiCacheIAMTokenIsReusedWithinItsValidity(t *testing.T) {
	credentials := staticAwsCredentials()
	provider := newElastiCacheIAMCredentialProvider(credentials, "us-east-1", "my-cache", "cache-user", false)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }

	_, first, err := provider.Credentials(context.Background())
	require.NoError(t, err)

	now = now.Add(13 * time.Minute)
	_, second, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, credentials.calls)

	now = now.Add(2 * time.Minute)
	_, third, err := provider.Credentials(context.Background())
	require.NoError(t, err)
	assert.NotEqual(t, first, third)
	assert.Equal(t, 2, credentials.calls)
}

func TestElastiCacheIAMTokenReuseStopsAtCredentialExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	credentials := staticAwsCredentials()
	credentials.credentials.CanExpire = true
	credentials.credentials.Expires = now.Add(5 * time.Minute)

	provider := newElastiCacheIAMCredentialProvider(credentials, "us-east-1", "my-cache", "cache-user", false)
	provider.now = func() time.Time { return now }

	_, first, err := provider.Credentials(context.Background())
	require.NoError(t, err)

	now = now.Add(4*time.Minute + 30*time.Second)
	_, second, err := provider.Credentials(context.Background())
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "token minted with credentials that are about to expire must not be reused")
	assert.Equal(t, 2, credentials.calls)
}

func TestElastiCacheIAMReportsCredentialRetrievalFailure(t *testing.T) {
	credentials := &stubAwsCredentialsProvider{err: errors.New("no EC2 IMDS role found")}
	provider := newElastiCacheIAMCredentialProvider(credentials, "us-east-1", "my-cache", "cache-user", false)

	_, _, err := provider.Credentials(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no EC2 IMDS role found")
}

func TestCredentialProviderFromSettingsRejectsIncompleteAwsIamConfig(t *testing.T) {
	assert.PanicsWithError(t, "REDIS_AWS_IAM_CACHE_NAME and REDIS_AWS_IAM_USER_ID are required when REDIS_AWS_IAM_AUTH is enabled", func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{RedisAwsIamAuth: true}, false)
	})

	assert.PanicsWithError(t, "REDIS_PERSECOND_AWS_IAM_CACHE_NAME and REDIS_PERSECOND_AWS_IAM_USER_ID are required when REDIS_PERSECOND_AWS_IAM_AUTH is enabled", func() {
		newCredentialProviderFromSettings(context.Background(), settings.Settings{
			RedisPerSecondAwsIamAuth:      true,
			RedisPerSecondAwsIamCacheName: "my-cache",
		}, true)
	})
}

func TestCredentialProviderFromSettingsRejectsAwsIamWithAnotherSource(t *testing.T) {
	s := settings.Settings{
		RedisAwsIamAuth:      true,
		RedisAwsIamCacheName: "my-cache",
		RedisAwsIamUserId:    "cache-user",
		RedisAuthFile:        "/run/secrets/redis-auth",
	}

	assert.PanicsWithError(t, "REDIS_AWS_IAM_AUTH cannot be combined with REDIS_AUTH or REDIS_AUTH_FILE", func() {
		newCredentialProviderFromSettings(context.Background(), s, false)
	})
}

func TestElastiCacheIAMTokenIsSharedByConcurrentDials(t *testing.T) {
	credentials := staticAwsCredentials()
	provider := newElastiCacheIAMCredentialProvider(credentials, "us-east-1", "my-cache", "cache-user", false)

	tokens := make([]string, 16)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, token, err := provider.Credentials(context.Background())
			require.NoError(t, err)
			tokens[i] = token
		}(i)
	}
	wg.Wait()

	for _, token := range tokens {
		assert.Equal(t, tokens[0], token)
	}
	assert.Equal(t, 1, credentials.calls)
}
