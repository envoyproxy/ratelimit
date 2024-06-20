package godogstats

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSeparateExtraTags(t *testing.T) {
	tests := []struct {
		name         string
		givenMetric  string
		expectOutput string
		expectTags   []string
	}{
		{
			name:         "no extra tags",
			givenMetric:  "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectOutput: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectTags:   nil,
		},
		{
			name:         "one extra tags",
			givenMetric:  "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT=12345",
			expectOutput: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectTags:   []string{"COMMIT:12345"},
		},
		{
			name:         "two extra tags",
			givenMetric:  "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT=12345.__DEPLOY=6890",
			expectOutput: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectTags:   []string{"COMMIT:12345", "DEPLOY:6890"},
		},
		{
			name:         "invalid extra tag no value",
			givenMetric:  "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT",
			expectOutput: "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectTags:   []string{},
		},
	}

	for _, tt := range tests {
		actualName, actualTags := separateTags(tt.givenMetric)

		assert.Equal(t, tt.expectOutput, actualName)
		assert.Equal(t, tt.expectTags, actualTags)
	}
}

func TestSinkMogrify(t *testing.T) {
	g := &godogStatsSink{
		mogrifier: mogrifierMap{
			{
				matcher: regexp.MustCompile(`^ratelimit\.(.*)$`),
				handler: func(matches []string) (string, []string) {
					return "custom." + matches[1], []string{"tag1:value1", "tag2:value2"}
				},
			},
		},
	}

	tests := []struct {
		name         string
		input        string
		expectedName string
		expectedTags []string
	}{
		{
			name:         "mogrify with match and extra tags",
			input:        "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT=12345.__DEPLOY=67890",
			expectedName: "custom.service.rate_limit.mongo_cps.database_users.total_hits",
			expectedTags: []string{"COMMIT:12345", "DEPLOY:67890", "tag1:value1", "tag2:value2"},
		},
		{
			name:         "mogrify with match without extra tags",
			input:        "ratelimit.service.rate_limit.mongo_cps.database_users.total_hits",
			expectedName: "custom.service.rate_limit.mongo_cps.database_users.total_hits",
			expectedTags: []string{"tag1:value1", "tag2:value2"},
		},
		{
			name:         "extra tags with no match",
			input:        "foo.service.rate_limit.mongo_cps.database_users.total_hits.__COMMIT=12345.__DEPLOY=67890",
			expectedName: "foo.service.rate_limit.mongo_cps.database_users.total_hits",
			expectedTags: []string{"COMMIT:12345", "DEPLOY:67890"},
		},
		{
			name:         "no mogrification",
			input:        "other.metric.name",
			expectedName: "other.metric.name",
			expectedTags: nil,
		},
	}

	for _, tt := range tests {
		actualName, actualTags := g.mogrify(tt.input)

		assert.Equal(t, tt.expectedName, actualName)
		assert.Equal(t, tt.expectedTags, actualTags)
	}
}
