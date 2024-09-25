package utils

import (
	"testing"
)

func TestSanitizeStatName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "domain.foo|bar",
			expected: "domain.foo_bar",
		},
		{
			input:    "domain.foo:bar",
			expected: "domain.foo_bar",
		},
		{
			input:    "domain.masked_remote_address_0.0.0.0/0",
			expected: "domain.masked_remote_address_0_0_0_0/0",
		},
		{
			input:    "domain.remote_address_172.18.0.1",
			expected: "domain.remote_address_172_18_0_1",
		},
		{
			input:    "domain.masked_remote_address_0.0.0.0/0_remote_address_172.18.0.1",
			expected: "domain.masked_remote_address_0_0_0_0/0_remote_address_172_18_0_1",
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			if actual := SanitizeStatName(c.input); actual != c.expected {
				t.Errorf("SanitizeStatName(%s): expected %s, actual %s", c.input, c.expected, actual)
			}
		})
	}
}
