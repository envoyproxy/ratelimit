package server

import (
	"crypto/x509"
	"errors"

	logger "github.com/sirupsen/logrus"
)

func verifyClient(clientCAPool *x509.CertPool, clientSAN string) func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		for _, certs := range verifiedChains {
			opts := x509.VerifyOptions{
				Roots:         clientCAPool,
				Intermediates: x509.NewCertPool(),
				DNSName:       clientSAN,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			if len(certs) < 1 {
				return errors.New("missing client cert")
			}
			// Get intermediates if any
			for _, cert := range certs[1:] {
				opts.Intermediates.AddCert(cert)
			}
			_, err := certs[0].Verify(opts)
			if err != nil {
				logger.Warnf("error validating client: %s", err.Error())
				return err
			}
		}
		return nil
	}
}
