package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	logger "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	gcpIAMTokenScope = "https://www.googleapis.com/auth/cloud-platform"
	// Fetch a new token this long before the current one expires, so a
	// connection never authenticates with a token that is about to lapse.
	gcpIAMTokenEarlyRefresh = 5 * time.Minute
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
	if token.AccessToken == "" {
		return "", "", errors.New("retrieving a Google access token for Memorystore IAM authentication: the token is empty")
	}

	return "", token.AccessToken, nil
}

func newGcpIamCredentialProvider(ctx context.Context, prefix string) CredentialProvider {
	credentials, err := google.FindDefaultCredentialsWithParams(ctx, google.CredentialsParams{
		Scopes:            []string{gcpIAMTokenScope},
		EarlyTokenRefresh: gcpIAMTokenEarlyRefresh,
	})
	if err != nil {
		panic(RedisError(fmt.Sprintf("%s_GCP_IAM_AUTH is enabled but no Google credentials were found: %v", prefix, err)))
	}

	logger.Warnf("enabling GCP IAM authentication to Memorystore for %s", prefix)

	return &gcpIAMCredentialProvider{tokens: credentials.TokenSource}
}
