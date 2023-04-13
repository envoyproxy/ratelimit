package utils

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

type CAType int

const (
	ClientCA CAType = iota
	ServerCA
)

// TlsConfigFromFiles sets the TLS config from the provided files.
func TlsConfigFromFiles(certFile, keyFile, caCertFile string, caType CAType, skipHostnameVerification bool) *tls.Config {
	config := &tls.Config{
		InsecureSkipVerify: skipHostnameVerification,
	}
	if skipHostnameVerification {
		// Based upon https://github.com/golang/go/blob/d67d044310bc5cc1c26b60caf23a58602e9a1946/src/crypto/tls/example_test.go#L187
		config.VerifyPeerCertificate = func(certificates [][]byte, verifiedChains [][]*x509.Certificate) error {
			certs := make([]*x509.Certificate, len(certificates))
			for i, asn1Data := range certificates {
				cert, err := x509.ParseCertificate(asn1Data)
				if err != nil {
					return errors.New("tls: failed to parse certificate from server: " + err.Error())
				}
				certs[i] = cert
			}

			opts := x509.VerifyOptions{
				Roots:         config.RootCAs,
				DNSName:       "",
				Intermediates: x509.NewCertPool(),
			}
			for _, cert := range certs[1:] {
				opts.Intermediates.AddCert(cert)
			}
			_, err := certs[0].Verify(opts)
			return err
		}
	}

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
