package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

type CAType int

const (
	ClientCA CAType = iota
	ServerCA
)

// TlsConfigFromFiles sets the TLS config from the provided files.
func TlsConfigFromFiles(certFile, keyFile, caCertFile string, caType CAType) *tls.Config {
	config := &tls.Config{}
	if certFile != "" && keyFile != "" {
		tlsKeyPair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			panic(fmt.Errorf("failed lo load TLS key pair (%s,%s): %w", certFile, keyFile, err))
		}
		config.Certificates = append(config.Certificates, tlsKeyPair)
	}
	if caCertFile != "" {
		// try to get the SystemCertPool first
		certPool, _ := x509.SystemCertPool()
		if certPool == nil {
			certPool = x509.NewCertPool()
		}
		if !certPool.AppendCertsFromPEM(mustReadFile(caCertFile)) {
			panic(fmt.Errorf("failed to load the provided TLS CA certificate: %s", caCertFile))
		}
		switch caType {
		case ClientCA:
			config.ClientCAs = certPool
		case ServerCA:
			config.RootCAs = certPool
		}
	}
	return config
}

func mustReadFile(name string) []byte {
	b, err := os.ReadFile(name)
	if err != nil {
		panic(fmt.Errorf("failed to read file: %s: %w", name, err))
	}
	return b
}
