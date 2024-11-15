package memberoperatorconfig

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestSecretToMemberOperatorConfigMapper(t *testing.T) {
	// when
	ctx := context.TODO()
	secretData := map[string][]byte{
		"che-admin-username": []byte("cheadmin"),
		"che-admin-password": []byte("password"),
	}

	t.Run("test secret maps correctly", func(t *testing.T) {
		// given
		secret := newSecret("test-secret", secretData)

		// when
		req := MapSecretToMemberOperatorConfig()(ctx, secret)

		// then
		require.Len(t, req, 1)
		require.Equal(t, types.NamespacedName{
			Namespace: test.MemberOperatorNs,
			Name:      "config",
		}, req[0].NamespacedName)
	})

	t.Run("a non-secret resource is not mapped", func(t *testing.T) {
		// given
		pod := &corev1.Pod{}

		// when
		req := MapSecretToMemberOperatorConfig()(ctx, pod)

		// then
		assert.Empty(t, req)
	})
}

func newSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: test.MemberOperatorNs,
		},
		Data: data,
	}
}
