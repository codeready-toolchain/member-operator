// this code was inspired by https://github.com/knative/pkg

package cert

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateSecret(t *testing.T) {
	// when
	secret, err := newSecret("foo", "ns", "bar", Expiration)

	// then
	require.NoError(t, err)
	assert.NotEmpty(t, secret.Data[ServerKey])
	assert.NotEmpty(t, secret.Data[ServerCert])
	assert.NotEmpty(t, secret.Data[CACert])
}

func TestEnsureCertSecret(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	t.Run("when secret doesn't exist yet", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, Expiration)

		// then
		require.NoError(t, err)
		assert.NotEmpty(t, caCert)
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, func() {
			assert.NotEmpty(t, actualSecret.Data[ServerKey])
			assert.NotEmpty(t, actualSecret.Data[ServerCert])
			assert.Equal(t, caCert, actualSecret.Data[CACert])
		})
	})

	t.Run("when secret already exists with wrong values", func(t *testing.T) {
		// given
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: test.MemberOperatorNs,
				Name:      "webhook-certs",
			},
			Data: map[string][]byte{
				"some":        []byte("data"),
				"ca-cert.pem": []byte("ca-cert-data"),
			},
		}
		fakeClient := test.NewFakeClient(t, secret)

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, Expiration)

		// then
		require.NoError(t, err)
		assert.NotEqual(t, "ca-cert-data", string(caCert))
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, func() {
			assert.NotEmpty(t, actualSecret.Data[ServerKey])
			assert.NotEmpty(t, actualSecret.Data[ServerCert])
			assert.Equal(t, caCert, actualSecret.Data[CACert])

			assert.NotEqual(t, secret.Data[ServerKey], actualSecret.Data[ServerKey])
			assert.NotEqual(t, secret.Data[ServerCert], actualSecret.Data[ServerCert])
			assert.NotEqual(t, secret.Data[CACert], actualSecret.Data[CACert])
		})
	})

	t.Run("when secret already exists but has expired", func(t *testing.T) {
		// given
		shortExpiration := time.Duration(time.Second)
		secret, err := newSecret(certSecretName, test.MemberOperatorNs, serviceName, shortExpiration)
		require.NoError(t, err)
		fakeClient := test.NewFakeClient(t, secret)
		time.Sleep(shortExpiration / 2)

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, shortExpiration)

		// then
		require.NoError(t, err)
		assert.NotEqual(t, secret.Data[CACert], caCert)
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, func() {
			assert.NotEmpty(t, actualSecret.Data[ServerKey])
			assert.NotEmpty(t, actualSecret.Data[ServerCert])
			assert.Equal(t, caCert, actualSecret.Data[CACert])

			assert.NotEqual(t, secret.Data[ServerKey], actualSecret.Data[ServerKey])
			assert.NotEqual(t, secret.Data[ServerCert], actualSecret.Data[ServerCert])
			assert.NotEqual(t, secret.Data[CACert], actualSecret.Data[CACert])
		})
	})

	t.Run("when secret already exists and is not yet expired", func(t *testing.T) {
		// given
		secret, err := newSecret(certSecretName, test.MemberOperatorNs, serviceName, Expiration)
		require.NoError(t, err)
		fakeClient := test.NewFakeClient(t, secret)

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, Expiration)

		// then
		require.NoError(t, err)
		assert.Equal(t, secret.Data[CACert], caCert)
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, func() {
			assert.Equal(t, secret.Data[ServerKey], actualSecret.Data[ServerKey])
			assert.Equal(t, secret.Data[ServerCert], actualSecret.Data[ServerCert])
			assert.Equal(t, secret.Data[CACert], actualSecret.Data[CACert])
		})
	})

	t.Run("when cannot get the secret", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
			return fmt.Errorf("some error")
		}

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, Expiration)
		fmt.Println()

		// then
		fakeClient.MockGet = nil
		require.Error(t, err)
		assert.Empty(t, caCert)
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, nil)
	})

	t.Run("when cannot create the secret", func(t *testing.T) {
		// given
		fakeClient := test.NewFakeClient(t)
		fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		caCert, err := EnsureSecret(fakeClient, test.MemberOperatorNs, Expiration)

		// then
		require.Error(t, err)
		assert.Empty(t, caCert)
		actualSecret := &v1.Secret{}
		AssertMemberObject(t, fakeClient, "webhook-certs", actualSecret, nil)
	})
}
