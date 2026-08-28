package redis

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mediocregopher/radix/v4"

	"github.com/envoyproxy/ratelimit/src/settings"
)

type CredentialProvider interface {
	Credentials(ctx context.Context) (user, pass string, err error)
}

func wrapDialerCredentialProvider(base radix.Dialer, provider CredentialProvider) radix.Dialer {
	if base.CustomConn != nil {
		panic(RedisError("the Redis credential provider must wrap the dialer before any CustomConn dialer, otherwise connections are not authenticated"))
	}

	return radix.Dialer{
		CustomConn: func(ctx context.Context, network, addr string) (radix.Conn, error) {
			user, pass, err := provider.Credentials(ctx)
			if err != nil {
				return nil, err
			}

			dialer := base
			dialer.AuthUser = user
			dialer.AuthPass = pass
			return dialer.Dial(ctx, network, addr)
		},
	}
}

func splitAuth(auth string) (user, pass string, hasUser bool) {
	user, pass, hasUser = strings.Cut(auth, ":")
	if !hasUser {
		return "", auth, false
	}
	return user, pass, true
}

type fileCredentialProvider struct {
	path string
}

func newFileCredentialProvider(path string) *fileCredentialProvider {
	return &fileCredentialProvider{path: path}
}

func (p *fileCredentialProvider) Credentials(_ context.Context) (string, string, error) {
	contents, err := os.ReadFile(p.path)
	if err != nil {
		return "", "", err
	}

	auth := strings.TrimSpace(string(contents))
	if auth == "" {
		return "", "", fmt.Errorf("redis credential file %s is empty", p.path)
	}

	user, pass, _ := splitAuth(auth)
	return user, pass, nil
}

func newCredentialProviderFromSettings(s settings.Settings, perSecond bool) CredentialProvider {
	prefix, auth, authFile := "REDIS", s.RedisAuth, s.RedisAuthFile
	if perSecond {
		prefix, auth, authFile = "REDIS_PERSECOND", s.RedisPerSecondAuth, s.RedisPerSecondAuthFile
	}

	if authFile == "" {
		return nil
	}

	if auth != "" {
		panic(RedisError(fmt.Sprintf("%s_AUTH and %s_AUTH_FILE are mutually exclusive", prefix, prefix)))
	}

	return newFileCredentialProvider(authFile)
}
