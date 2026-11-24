package redis

import (
	"context"
	"fmt"
	"net/http"
	"time"

	logger "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	gcpIAMTokenScope = "https://www.googleapis.com/auth/cloud-platform"
	// Fetch a new token this long before the current one expires, so a
	// connection never authenticates with a token that is about to lapse.
	// Google's library only honors this for metadata server tokens; tokens
	// from a credentials file refresh 10 seconds before expiry.
	gcpIAMTokenEarlyRefresh = 5 * time.Minute
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
}

func (p *gcpIAMCredentialProvider) Credentials(_ context.Context) (string, string, error) {
	token, err := p.tokens.Token()
	if err != nil {
		return "", "", fmt.Errorf("retrieving a Google access token for Memorystore IAM authentication: %w", err)
	}
	return "", token.AccessToken, nil
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
