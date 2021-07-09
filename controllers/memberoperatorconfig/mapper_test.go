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
	secret := newSecret("test-secret", secretData)

	t.Run("test secret maps correctly", func(t *testing.T) {
		// given
		c := test.NewFakeClient(t)

		// when
		req := MapSecretToMemberOperatorConfig(c)(secret)

		// then
		require.Len(t, req, 1)
		require.Equal(t, types.NamespacedName{
			Namespace: test.MemberOperatorNs,
			Name:      "config",
		}, req[0].NamespacedName)
	})

	t.Run("a non-secret resource is not mapped", func(t *testing.T) {
		// given
		c := test.NewFakeClient(t)
		pod := &corev1.Pod{}

		// when
		req := MapSecretToMemberOperatorConfig(c)(pod)

		require.Len(t, req, 0)
	})
}
