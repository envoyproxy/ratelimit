package redis

import (
	"context"
	"fmt"
	"net/http"
	"time"

	logger "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/singleflight"
)

const (
	gcpIAMTokenScope = "https://www.googleapis.com/auth/cloud-platform"
	// Fetch a new token this long before the current one expires. It stays
	// under the metadata server's shortest token cache (4 minutes), matching
	// cloud.google.com/go/auth. Google's library only honors this for metadata
	// server tokens; tokens from a credentials file refresh 10 seconds early.
	gcpIAMTokenEarlyRefresh = 225 * time.Second
	// Bounds a token request made with a credentials file, which otherwise
	// uses an HTTP client with no timeout. The metadata server path has its
	// own client with timeouts.
	gcpIAMTokenRequestTimeout = 10 * time.Second
)

// gcpIAMCredentialProvider authenticates to GCP Memorystore with a Google
// access token as the password and no username, which Memorystore accepts as
// the default user. The token source caches the token and is safe for
// concurrent use, so every connection in the pool shares one token.
type gcpIAMCredentialProvider struct {
	tokens oauth2.TokenSource
	fetch  singleflight.Group
}

func (p *gcpIAMCredentialProvider) Credentials(ctx context.Context) (string, string, error) {
	// Token takes no context and a metadata server fetch can retry for tens of
	// seconds, so give up when the dial does. Concurrent dials share one fetch,
	// which keeps going after they give up and fills the cache for later dials.
	result := p.fetch.DoChan("token", func() (any, error) {
		token, err := p.tokens.Token()
		if err != nil {
			// Logged here because a dial that already gave up never sees it.
			logger.Warnf("Memorystore IAM authentication: getting a Google access token failed: %v", err)
			return nil, err
		}
		return token.AccessToken, nil
	})

	select {
	case r := <-result:
		if r.Err != nil {
			return "", "", fmt.Errorf("retrieving a Google access token for Memorystore IAM authentication: %w", r.Err)
		}
		return "", r.Val.(string), nil
	case <-ctx.Done():
		return "", "", fmt.Errorf("retrieving a Google access token for Memorystore IAM authentication: %w", ctx.Err())
	}
}

func newGcpIamCredentialProvider(ctx context.Context, prefix string) CredentialProvider {
	// Some credential types keep this context for every later token request,
	// so it must outlive shutdown (the runner cancels ctx on SIGTERM) and
	// carry an HTTP client with a timeout.
	tokenCtx := context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, &http.Client{Timeout: gcpIAMTokenRequestTimeout})
	credentials, err := google.FindDefaultCredentialsWithParams(tokenCtx, google.CredentialsParams{
		Scopes:            []string{gcpIAMTokenScope},
		EarlyTokenRefresh: gcpIAMTokenEarlyRefresh,
	})
	if err != nil {
		panic(RedisError(fmt.Sprintf("%s_GCP_IAM_AUTH is enabled but no Google credentials were found: %v", prefix, err)))
	}

	logger.Warnf("enabling GCP IAM authentication to Memorystore for %s", prefix)

	return &gcpIAMCredentialProvider{tokens: credentials.TokenSource}
}
