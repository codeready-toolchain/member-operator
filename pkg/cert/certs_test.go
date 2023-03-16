// most of the code was copied from https://github.com/knative/pkg

package cert_test

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/cert"
	"github.com/google/go-cmp/cmp"
)

func TestCreateCerts(t *testing.T) {
	sKey, serverCertPEM, caCertBytes, err := cert.CreateCerts("got-the-service", "cool-service", time.Now().AddDate(1, 0, 0))
	if err != nil {
		t.Fatal("Failed to create certs", err)
	}

	// Test server private key
	p, _ := pem.Decode(sKey)
	if p.Type != "RSA PRIVATE KEY" {
		t.Fatal("Expected the key to be RSA Private key type")
	}
	key, err := x509.ParsePKCS1PrivateKey(p.Bytes)
	if err != nil {
		t.Fatal("Failed to parse private key", err)
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("Failed to validate private key")
	}

	// Test Server Cert
	sCert, err := validCertificate(serverCertPEM, t)
	if err != nil {
		t.Fatal(err)
	}

	// Test CA Cert
	caParsedCert, err := validCertificate(caCertBytes, t)
	if err != nil {
		t.Fatal(err)
	}

	// Verify common name
	const expectedCommonName = "got-the-service.cool-service.svc"

	if caParsedCert.Subject.CommonName != expectedCommonName {
		t.Fatalf("Unexpected Cert Common Name %q, wanted %q", caParsedCert.Subject.CommonName, expectedCommonName)
	}

	// Verify domain names
	expectedDNSNames := []string{
		"got-the-service",
		"got-the-service.cool-service",
		"got-the-service.cool-service.svc",
		"got-the-service.cool-service.svc.cluster.local",
	}
	if diff := cmp.Diff(caParsedCert.DNSNames, expectedDNSNames); diff != "" {
		t.Fatal("Unexpected CA Cert DNS Name (-want +got) :", diff)
	}

	if diff := cmp.Diff(caParsedCert.DNSNames, expectedDNSNames); diff != "" {
		t.Fatal("Unexpected CA Cert DNS Name (-want +got):", diff)
	}

	// Verify Server Cert is Signed by CA Cert
	if err = sCert.CheckSignatureFrom(caParsedCert); err != nil {
		t.Fatal("Failed to verify that the signature on server certificate is from parent CA cert", err)
	}
}

func validCertificate(cert []byte, t *testing.T) (*x509.Certificate, error) {
	t.Helper()
	const certificate = "CERTIFICATE"
	caCert, _ := pem.Decode(cert)
	if caCert.Type != certificate {
		return nil, fmt.Errorf("cert.Type = %s, want: %s", caCert.Type, certificate)
	}
	parsedCert, err := x509.ParseCertificate(caCert.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cert %w", err)
	}
	if parsedCert.SignatureAlgorithm != x509.SHA256WithRSA {
		return nil, fmt.Errorf("failed to match signature. Got: %s, want: %s", parsedCert.SignatureAlgorithm, x509.SHA256WithRSA)
	}
	return parsedCert, nil
}
