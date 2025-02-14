package useraccount

import (
	"context"
	"fmt"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestDeleteConsoleSettingObjects(t *testing.T) {
	t.Run("Object found by name and deleted", func(t *testing.T) {
		// given
		ctx := context.Background()
		cm := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-johnsmith",
				Namespace: UserSettingNS,
			},
		}
		cl := test.NewFakeClient(t, cm)

		// when
		err := deleteResource(ctx, cl, "johnsmith", &corev1.ConfigMap{})

		// then
		require.NoError(t, err)
		// check that the configmap doesn't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-johnsmith", &corev1.ConfigMap{})
	})
	t.Run("Role found by name and deleted", func(t *testing.T) {
		// given
		ctx := context.Background()
		role := &rbac.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-johnsmith-role",
				Namespace: UserSettingNS,
			},
		}
		cl := test.NewFakeClient(t, role)

		// when
		err := deleteResource(ctx, cl, "johnsmith", &rbac.Role{TypeMeta: metav1.TypeMeta{Kind: "Role"}})

		// then
		require.NoError(t, err)
		// check that the role doesn't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-johnsmith-role", &rbac.Role{})
	})

	t.Run("Rolebinding found by name and deleted", func(t *testing.T) {
		// given
		ctx := context.Background()
		rb := &rbac.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-johnsmith-rolebinding",
				Namespace: UserSettingNS,
			},
		}
		cl := test.NewFakeClient(t, rb)

		// when
		err := deleteResource(ctx, cl, "johnsmith", &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"}})

		// then
		require.NoError(t, err)
		// check that the rolebinding doesn't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-johnsmith-rolebinding", &rbac.RoleBinding{})
	})

	t.Run("Error is returned when error in deleting configmap", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t)

		cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("error in deleting configmap")
		}

		// when
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})

		// then
		require.Error(t, err)
		require.Equal(t, "error in deleting configmap", err.Error())
	})
	t.Run("No Error is returned when no object is found", func(t *testing.T) {
		// given
		noiseObject := &rbac.Role{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Role",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-johnsmith",
				Namespace: UserSettingNS,
			},
		}
		cl := test.NewFakeClient(t, noiseObject)
		// when
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
		// then
		require.NoError(t, err)
	})
}
