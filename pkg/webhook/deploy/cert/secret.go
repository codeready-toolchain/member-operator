// this code was inspired by https://github.com/knative/pkg

package cert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ServerKey is the name of the key associated with the secret's private key.
	ServerKey = "server-key.pem"
	// ServerCert is the name of the key associated with the secret's public key.
	ServerCert = "server-cert.pem"
	// CACert is the name of the key associated with the certificate of the CA for
	// the keypair.
	CACert = "ca-cert.pem"

	// certSecretName is a name of the secret
	certSecretName = "webhook-certs" // nolint:gosec

	// Expiration is a default duration after which the certificates should expire
	Expiration = 365 * 24 * time.Hour

	// serviceName is the name of webhook service
	serviceName = "member-operator-webhook"
)

func EnsureSecret(cl client.Client, namespace string, expiration time.Duration) ([]byte, error) {
	certSecret := &corev1.Secret{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: certSecretName}, certSecret); err != nil && !errors.IsNotFound(err) {
		return nil, err
	} else if err != nil {
		// does not exist, so let's create it
		certSecret, err := newSecret(certSecretName, namespace, serviceName, expiration)
		if err != nil {
			return nil, err
		}
		if err := cl.Create(context.TODO(), certSecret); err != nil {
			return nil, err
		}
		return certSecret.Data[CACert], nil
	}

	// already exists - check the expiration date of the certificate to see if it needs to be updated
	cert, err := tls.X509KeyPair(certSecret.Data[ServerCert], certSecret.Data[ServerKey])
	if err != nil {
		log.Error(err, "creating pem from certificate and key failed")
	} else {
		certData, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			log.Error(err, "parsing certificate failed; will update the secret with a freshly generated cert")
		} else if time.Now().Add(expiration / 2).Before(certData.NotAfter) {
			// expiration is fine
			return certSecret.Data[CACert], nil
		}
	}

	// let's update the secret with certificates
	newSecret, err := newSecret(certSecretName, namespace, serviceName, expiration)
	if err != nil {
		return nil, err
	}
	newSecret.SetResourceVersion(certSecret.GetResourceVersion())
	if err := cl.Update(context.TODO(), newSecret); err != nil {
		return nil, err
	}
	return newSecret.Data[CACert], nil
}

// newSecret creates a secret containing certificate data
func newSecret(name, namespace, serviceName string, expiration time.Duration) (*corev1.Secret, error) {
	serverKey, serverCert, caCert, err := CreateCerts(serviceName, namespace, time.Now().Add(expiration))
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			ServerKey:  serverKey,
			ServerCert: serverCert,
			CACert:     caCert,
		},
	}, nil
}
