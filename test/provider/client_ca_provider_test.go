package provider_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ratelimit/src/provider"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func writeCA(t *testing.T, path, cn string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	// Write to a temp file and rename it so the CA file is replaced atomically
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+filepath.Base(path))
	require.NoError(t, os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.Rename(tmp, path))
}

func TestClientCAProviderReload(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	writeCA(t, caFile, "initial-ca")
	initialPool, err := utils.LoadCertPool(caFile)
	require.NoError(t, err)

	p := provider.NewClientCAProvider(settings.Settings{}, gostats.NewStore(gostats.NewNullSink(), false), caFile)
	assert.True(t, p.ClientCAs().Equal(initialPool))

	writeCA(t, caFile, "rotated-ca")
	rotatedPool, err := utils.LoadCertPool(caFile)
	require.NoError(t, err)
	assert.False(t, rotatedPool.Equal(initialPool))

	assert.Eventually(t, func() bool {
		return p.ClientCAs().Equal(rotatedPool)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClientCAProviderKeepsCAOnInvalidUpdate(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	writeCA(t, caFile, "initial-ca")
	initialPool, err := utils.LoadCertPool(caFile)
	require.NoError(t, err)

	p := provider.NewClientCAProvider(settings.Settings{}, gostats.NewStore(gostats.NewNullSink(), false), caFile)

	// Replace the CA with an invalid file, the old CA must be kept
	tmp := filepath.Join(filepath.Dir(caFile), ".tmp-ca.crt")
	require.NoError(t, os.WriteFile(tmp, []byte("not a certificate"), 0o600))
	require.NoError(t, os.Rename(tmp, caFile))
	assert.Never(t, func() bool {
		return !p.ClientCAs().Equal(initialPool)
	}, time.Second, 100*time.Millisecond)

	// A valid CA written after the invalid one is still picked up, so the watcher kept running
	writeCA(t, caFile, "rotated-ca")
	rotatedPool, err := utils.LoadCertPool(caFile)
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		return p.ClientCAs().Equal(rotatedPool)
	}, 10*time.Second, 100*time.Millisecond)
}
