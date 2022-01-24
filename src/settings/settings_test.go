package settings

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSettingsTlsConfigUnmodified(t *testing.T) {
	settings := NewSettings()
	assert.NotNil(t, settings.RedisTlsConfig)
	assert.Nil(t, settings.RedisTlsConfig.RootCAs)
}
