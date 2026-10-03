package redis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	logger "github.com/sirupsen/logrus"
)

const (
	elastiCacheIAMSigningService   = "elasticache"
	elastiCacheIAMTokenValidity    = 15 * time.Minute
	elastiCacheIAMTokenReuseMargin = time.Minute
	emptyPayloadHash               = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type elastiCacheIAMCredentialProvider struct {
	credentials aws.CredentialsProvider
	signer      *v4.Signer
	region      string
	cacheName   string
	userID      string
	serverless  bool
	now         func() time.Time

	mu         sync.Mutex
	token      string
	reuseUntil time.Time
}

func newElastiCacheIAMCredentialProvider(credentials aws.CredentialsProvider, region, cacheName, userID string, serverless bool) *elastiCacheIAMCredentialProvider {
	return &elastiCacheIAMCredentialProvider{
		credentials: credentials,
		signer:      v4.NewSigner(),
		region:      region,
		cacheName:   strings.ToLower(cacheName),
		userID:      userID,
		serverless:  serverless,
		now:         time.Now,
	}
}

func (p *elastiCacheIAMCredentialProvider) Credentials(ctx context.Context) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if p.token != "" && now.Before(p.reuseUntil) {
		return p.userID, p.token, nil
	}

	credentials, err := p.credentials.Retrieve(ctx)
	if err != nil {
		return "", "", fmt.Errorf("retrieving AWS credentials for ElastiCache IAM authentication: %w", err)
	}

	token, err := p.buildToken(ctx, credentials, now)
	if err != nil {
		return "", "", err
	}

	p.token = token
	p.reuseUntil = tokenReuseDeadline(now, credentials)

	return p.userID, p.token, nil
}

func tokenReuseDeadline(now time.Time, credentials aws.Credentials) time.Time {
	deadline := now.Add(elastiCacheIAMTokenValidity - elastiCacheIAMTokenReuseMargin)

	if credentials.CanExpire {
		if credentialDeadline := credentials.Expires.Add(-elastiCacheIAMTokenReuseMargin); credentialDeadline.Before(deadline) {
			return credentialDeadline
		}
	}

	return deadline
}

func (p *elastiCacheIAMCredentialProvider) buildToken(ctx context.Context, credentials aws.Credentials, now time.Time) (string, error) {
	query := url.Values{
		"Action":        []string{"connect"},
		"User":          []string{p.userID},
		"X-Amz-Expires": []string{strconv.Itoa(int(elastiCacheIAMTokenValidity.Seconds()))},
	}
	if p.serverless {
		query.Set("ResourceType", "ServerlessCache")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+p.cacheName+"/?"+query.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("building the ElastiCache IAM authentication request: %w", err)
	}

	signedURI, _, err := p.signer.PresignHTTP(ctx, credentials, req, emptyPayloadHash, elastiCacheIAMSigningService, p.region, now.UTC())
	if err != nil {
		return "", fmt.Errorf("signing the ElastiCache IAM authentication token: %w", err)
	}

	return strings.TrimPrefix(signedURI, "http://"), nil
}

func newAwsIamCredentialProvider(ctx context.Context, prefix, cacheName, userID, region string, serverless bool) CredentialProvider {
	if cacheName == "" || userID == "" {
		panic(RedisError(fmt.Sprintf("%s_AWS_IAM_CACHE_NAME and %s_AWS_IAM_USER_ID are required when %s_AWS_IAM_AUTH is enabled", prefix, prefix, prefix)))
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	checkError(err)

	if region == "" {
		region = cfg.Region
	}
	if region == "" {
		panic(RedisError("REDIS_AWS_IAM_REGION is required when the AWS SDK cannot resolve a region"))
	}

	logger.Warnf("enabling AWS IAM authentication to ElastiCache cache %s as user %s in %s", cacheName, userID, region)

	return newElastiCacheIAMCredentialProvider(cfg.Credentials, region, cacheName, userID, serverless)
}
