//go:build integration

package integration_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/envoyproxy/ratelimit/src/utils"
)

func createCA() (caFileName string, ca *x509.Certificate, pk *rsa.PrivateKey, err error) {
	ca = &x509.Certificate{
		SerialNumber: big.NewInt(2022),
		Subject: pkix.Name{
			Organization: []string{"Acme CA"},
			Country:      []string{"CA"},
			Province:     []string{"BC"},
			Locality:     []string{"Vancouver"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	caPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", nil, nil, err
	}
	// create the CA
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return "", nil, nil, err
	}
	caPEM := new(bytes.Buffer)
	pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})
	cafileName, err := writeContentToTempFile(caPEM.Bytes(), "ca.pem")
	return cafileName, ca, caPrivKey, err
}

func writeContentToTempFile(content []byte, filenameprefix string) (filename string, err error) {
	f, err := os.CreateTemp("", filenameprefix)
	if err != nil {
		return "", err
	}
	_, err = f.Write(content)
	if err != nil {
		return "", err
	}
	err = f.Close()
	return f.Name(), err
}

func signCert(caType utils.CAType, ca *x509.Certificate, caPK *rsa.PrivateKey) (certFile string, keyFile string, err error) {
	keyUsage := x509.ExtKeyUsageServerAuth
	name := "Server"
	var sn int64 = 2021
	if caType == utils.ClientCA {
		keyUsage = x509.ExtKeyUsageClientAuth
		name = "Client"
		sn = 2020
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(sn),
		Subject: pkix.Name{
			Organization: []string{name},
			Country:      []string{"CA"},
			Province:     []string{"BC"},
			Locality:     []string{"Vancouver"},
		},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(0, 5, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{keyUsage},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPK)
	if err != nil {
		return "", "", err
	}
	certPEM := new(bytes.Buffer)
	pem.Encode(certPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})
	certfileName, err := writeContentToTempFile(certPEM.Bytes(), fmt.Sprintf("%s-cert.pem", name))
	if err != nil {
		return "", "", err
	}
	certPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})

	pkFileName, err := writeContentToTempFile(certPrivKeyPEM.Bytes(), fmt.Sprintf("%s-key.pem", name))
	if err != nil {
		return "", "", err
	}
	return certfileName, pkFileName, nil
}

func mTLSSetup(caType utils.CAType) (caFile string, certFile string, keyFile string, err error) {
	caFile, serverCA, serverCApk, err := createCA()
	if err != nil {
		return "", "", "", err
	}
	certFile, keyFile, err = signCert(caType, serverCA, serverCApk)
	return caFile, certFile, keyFile, err
}

func Test_mTLSSetup(t *testing.T) {
	_, _, _, err := mTLSSetup(utils.ServerCA)
	if err != nil {
		t.Fatal(err)
	}
}
