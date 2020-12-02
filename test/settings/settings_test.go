package settings_test

import (
	"os"
	"testing"
	"time"

	"github.com/envoyproxy/ratelimit/src/settings"
)

func TestShouldReturnDefaultValueForRedisPipelineWindow(t *testing.T) {
	expectedValue := time.Duration(0) * time.Second

	s := settings.NewSettings()
	if s.RedisPipelineWindow != expectedValue {
		t.Errorf("Got %s, expected %s", s.RedisPipelineWindow, expectedValue)
	}
}

func TestShouldReturnCorrectValueForRedisPipelineWindow(t *testing.T) {
	os.Setenv("REDIS_PIPELINE_WINDOW", "100")

	expectedValue := time.Duration(100) * time.Second

	s := settings.NewSettings()
	if s.RedisPipelineWindow != expectedValue {
		t.Errorf("Got %s, expected %s", s.RedisPipelineWindow, expectedValue)
	}
}

func TestShouldReturnDefaultValueForRedisPerSecondPipelineWindow(t *testing.T) {
	expectedValue := time.Duration(0) * time.Second

	s := settings.NewSettings()
	if s.RedisPerSecondPipelineWindow != expectedValue {
		t.Errorf("Got %s, expected %s", s.RedisPerSecondPipelineWindow, expectedValue)
	}
}

func TestShouldReturnCorrectValueForRedisPerSecondPipelineWindow(t *testing.T) {
	os.Setenv("REDIS_PERSECOND_PIPELINE_WINDOW", "200")

	expectedValue := time.Duration(200) * time.Second

	s := settings.NewSettings()
	if s.RedisPerSecondPipelineWindow != expectedValue {
		t.Errorf("Got %s, expected %s", s.RedisPerSecondPipelineWindow, expectedValue)
	}
}
