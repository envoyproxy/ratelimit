package settings

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSettingsTlsConfigUnmodified(t *testing.T) {
	settings := NewSettings()
	assert.NotNil(t, settings.RedisTlsConfig)
	assert.Nil(t, settings.RedisTlsConfig.RootCAs)
}
