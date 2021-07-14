package memberoperatorconfig

import (
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestSecretToMemberOperatorConfigMapper(t *testing.T) {
	// when
	secretData := map[string][]byte{
		"che-admin-username": []byte("cheadmin"),
		"che-admin-password": []byte("password"),
	}

	t.Run("test secret maps correctly", func(t *testing.T) {
		// given
		secret := newSecret("test-secret", secretData)

		// when
		req := MapSecretToMemberOperatorConfig()(secret)

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
		req := MapSecretToMemberOperatorConfig()(pod)

		// then
		require.Len(t, req, 0)
	})
}
