package memberoperatorconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSecretToMemberOperatorConfigMapper(t *testing.T) {
	// when
	secretData := map[string][]byte{
		"che-admin-username": []byte("cheadmin"),
		"che-admin-password": []byte("password"),
	}
	secret := newSecret("test-secret", secretData)
	config := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))

	t.Run("test secret maps correctly", func(t *testing.T) {
		c := test.NewFakeClient(t, config)

		mapper := &SecretToMemberOperatorConfigMapper{
			client: c,
		}

		// This is required for the mapper to function
		restore := test.SetEnvVarAndRestore(t, k8sutil.WatchNamespaceEnvVar, test.MemberOperatorNs)
		defer restore()

		req := mapper.Map(handler.MapObject{
			Object: secret,
		})

		require.Len(t, req, 1)
		require.Equal(t, types.NamespacedName{
			Namespace: config.Namespace,
			Name:      config.Name,
		}, req[0].NamespacedName)
	})

	t.Run("test secret from another namespace is not mapped", func(t *testing.T) {
		c := test.NewFakeClient(t, config)

		mapper := &SecretToMemberOperatorConfigMapper{
			client: c,
		}

		// This is required for the mapper to function
		restore := test.SetEnvVarAndRestore(t, k8sutil.WatchNamespaceEnvVar, test.MemberOperatorNs)
		defer restore()

		secretFromAnotherNamespace := secret.DeepCopy()
		secretFromAnotherNamespace.Namespace = "default"
		req := mapper.Map(handler.MapObject{
			Object: secretFromAnotherNamespace,
		})

		require.Len(t, req, 0)
	})

	t.Run("test SecretToMemberOperatorConfigMapper returns nil when client list fails", func(t *testing.T) {
		c := test.NewFakeClient(t)
		c.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return errors.New("err happened")
		}

		mapper := &SecretToMemberOperatorConfigMapper{
			client: c,
		}
		req := mapper.Map(handler.MapObject{
			Object: secret,
		})

		require.Len(t, req, 0)
	})

	t.Run("test SecretToMemberOperatorConfigMapper returns nil when watch namespace not set ", func(t *testing.T) {
		c := test.NewFakeClient(t)

		mapper := &SecretToMemberOperatorConfigMapper{
			client: c,
		}
		req := mapper.Map(handler.MapObject{
			Object: secret,
		})

		require.Len(t, req, 0)
	})
}
