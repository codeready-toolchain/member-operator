package cert_test

import (
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSecret(t *testing.T) {
	// when
	secret, err := cert.CreateSecret("foo", "ns", "bar")

	// then
	require.NoError(t, err)
	assert.NotEmpty(t, secret.Data[cert.ServerKey])
	assert.NotEmpty(t, secret.Data[cert.ServerCert])
	assert.NotEmpty(t, secret.Data[cert.CACert])
}
