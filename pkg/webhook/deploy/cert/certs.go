/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cert

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	organization      = "redhat.com"
	resolverFileName  = "/etc/resolv.conf"
	defaultDomainName = "cluster.local"
)

var (
	domainName = defaultDomainName
	once       sync.Once
	log        = logf.Log.WithName("certificate_manager")
)

// Create the common parts of the cert. These don't change between
// the root/CA cert and the server cert.
func createCertTemplate(name, namespace string, notAfter time.Time) (*x509.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, errors.New("failed to generate serial number: " + err.Error())
	}

	serviceName := name + "." + namespace
	commonName := serviceName + ".svc"
	serviceHostname := fmt.Sprintf("%s.%s.svc.%s", name, namespace, getClusterDomainName())
	serviceNames := []string{
		name,
		serviceName,
		commonName,
		serviceHostname,
	}

	tmpl := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{organization},
			CommonName:   commonName,
		},
		SignatureAlgorithm:    x509.SHA256WithRSA,
		NotBefore:             time.Now(),
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		DNSNames:              serviceNames,
	}
	return &tmpl, nil
}

// Create cert template suitable for CA and hence signing
func createCACertTemplate(name, namespace string, notAfter time.Time) (*x509.Certificate, error) {
	rootCert, err := createCertTemplate(name, namespace, notAfter)
	if err != nil {
		return nil, err
	}
	// Make it into a CA cert and change it so we can use it to sign certs
	rootCert.IsCA = true
	rootCert.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	rootCert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	return rootCert, nil
}

// Create cert template that we can use on the server for TLS
func createServerCertTemplate(name, namespace string, notAfter time.Time) (*x509.Certificate, error) {
	serverCert, err := createCertTemplate(name, namespace, notAfter)
	if err != nil {
		return nil, err
	}
	serverCert.KeyUsage = x509.KeyUsageDigitalSignature
	serverCert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	return serverCert, err
}

// Actually sign the cert and return things in a form that we can use later on
func createCert(template, parent *x509.Certificate, pub, parentPriv interface{}) (
	cert *x509.Certificate, certPEM []byte, err error) {

	certDER, err := x509.CreateCertificate(rand.Reader, template, parent, pub, parentPriv)
	if err != nil {
		return
	}
	cert, err = x509.ParseCertificate(certDER)
	if err != nil {
		return
	}
	b := pem.Block{Type: "CERTIFICATE", Bytes: certDER}
	certPEM = pem.EncodeToMemory(&b)
	return
}

func createCA(name, namespace string, notAfter time.Time) (*rsa.PrivateKey, *x509.Certificate, []byte, error) {
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Error(err, "error generating random key")
		return nil, nil, nil, err
	}

	rootCertTmpl, err := createCACertTemplate(name, namespace, notAfter)
	if err != nil {
		log.Error(err, "error generating CA cert")
		return nil, nil, nil, err
	}

	rootCert, rootCertPEM, err := createCert(rootCertTmpl, rootCertTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		log.Error(err, "error signing the CA cert")
		return nil, nil, nil, err
	}
	return rootKey, rootCert, rootCertPEM, nil
}

// CreateCerts creates and returns a CA certificate and certificate and
// key for the server. serverKey and serverCert are used by the server
// to establish trust for clients, CA certificate is used by the
// client to verify the server authentication chain. notAfter specifies
// the expiration date.
func CreateCerts(name, namespace string, notAfter time.Time) (serverKey, serverCert, caCert []byte, err error) {
	// First create a CA certificate and private key
	caKey, caCertificate, caCertificatePEM, err := createCA(name, namespace, notAfter)
	if err != nil {
		return nil, nil, nil, err
	}

	// Then create the private key for the serving cert
	servKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Error(err, "error generating random key")
		return nil, nil, nil, err
	}
	servCertTemplate, err := createServerCertTemplate(name, namespace, notAfter)
	if err != nil {
		log.Error(err, "failed to create the server certificate template")
		return nil, nil, nil, err
	}

	// create a certificate which wraps the server's public key, sign it with the CA private key
	_, servCertPEM, err := createCert(servCertTemplate, caCertificate, &servKey.PublicKey, caKey)
	if err != nil {
		log.Error(err, "error signing server certificate template")
		return nil, nil, nil, err
	}
	servKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(servKey),
	})
	return servKeyPEM, servCertPEM, caCertificatePEM, nil
}

// getClusterDomainName returns cluster's domain name or an error
// Closes issue: https://github.com/knative/eventing/issues/714
func getClusterDomainName() string {
	once.Do(func() {
		f, err := os.Open(resolverFileName)
		if err != nil {
			return
		}
		defer func() {
			if err := f.Close(); err != nil {
				log.Error(err, "unable to close", "file", resolverFileName)
			}
		}()
		domainName = getDomainName(f)
	})
	return domainName
}

func getDomainName(r io.Reader) string {
	for scanner := bufio.NewScanner(r); scanner.Scan(); {
		elements := strings.Split(scanner.Text(), " ")
		if elements[0] != "search" {
			continue
		}
		for _, e := range elements[1:] {
			if strings.HasPrefix(e, "svc.") {
				return strings.TrimSuffix(e[4:], ".")
			}
		}
	}
	// For all abnormal cases return default domain name.
	return defaultDomainName
}
