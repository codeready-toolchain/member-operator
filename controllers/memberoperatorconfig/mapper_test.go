package memberoperatorconfig

import (
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

func TestSecretToMemberOperatorConfigMapper(t *testing.T) {
	// when
	secretData := map[string][]byte{
		"che-admin-username": []byte("cheadmin"),
		"che-admin-password": []byte("password"),
	}
	secret := newSecret("test-secret", secretData)

	t.Run("test secret maps correctly", func(t *testing.T) {
		mapper := &SecretToMemberOperatorConfigMapper{}

		req := mapper.Map(handler.MapObject{
			Object: secret,
		})

		require.Len(t, req, 1)
		require.Equal(t, types.NamespacedName{
			Namespace: test.MemberOperatorNs,
			Name:      "config",
		}, req[0].NamespacedName)
	})

	t.Run("a non-secret resource is not mapped", func(t *testing.T) {
		mapper := &SecretToMemberOperatorConfigMapper{}

		pod := &corev1.Pod{}

		req := mapper.Map(handler.MapObject{
			Object: pod,
		})

		require.Len(t, req, 0)
	})
}
