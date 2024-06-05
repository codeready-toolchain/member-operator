package useraccount

import (
	"context"
	"fmt"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestDeleteConsoleSettingObjects(t *testing.T) {
	t.Run("Delete ConfigMap", func(t *testing.T) {
		t.Run("Configmap found by name and deleted", func(t *testing.T) {
			ctx := context.Background()
			cm := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-johnsmith",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(cm).Build()

			err := deleteResource(ctx, cl, "johnsmith", cm)
			require.NoError(t, err)
			// check that the configmap doesn't exist anymore
			err = cl.Get(ctx, client.ObjectKey{Name: "user-settings-johnsmith", Namespace: UserSettingNS}, &corev1.ConfigMap{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("Configmap found by label and deletes successfully", func(t *testing.T) {
			cm := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := fake.NewClientBuilder().WithObjects(cm).Build()
			err := deleteResource(context.TODO(), cl, "johnsmith", cm)
			require.NoError(t, err)
			// check that the configmap doesn't exist anymore
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &corev1.ConfigMap{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("multiple configmaps found by label and deletes successfully", func(t *testing.T) {
			cm1 := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cm2 := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match-second",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := fake.NewClientBuilder().WithObjects(cm1, cm2).Build()
			err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
			require.NoError(t, err)
			// check that the configmaps don't exist anymore
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &corev1.ConfigMap{})
			require.True(t, errors.IsNotFound(err))
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match-second", Namespace: UserSettingNS}, &corev1.ConfigMap{})
			require.True(t, errors.IsNotFound(err))
		})

		t.Run("Error is returned when error in deleting configmap", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting configmap")
			}
			err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
			require.Error(t, err)
			require.Equal(t, "error in deleting configmap", err.Error())
		})
		t.Run("No Error is returned when no configmap not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
			require.NoError(t, err)
		})

	})

	t.Run("Delete Role", func(t *testing.T) {
		t.Run("Role found by name and deleted", func(t *testing.T) {
			ctx := context.Background()
			role := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-johnsmith",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(role).Build()

			err := deleteResource(ctx, cl, "johnsmith", role)
			require.NoError(t, err)
			// check that the role was deleted
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-johnsmith", Namespace: UserSettingNS}, &rbac.Role{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("Role found by label and deleted successfully", func(t *testing.T) {
			role := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := test.NewFakeClient(t, role)
			err := deleteResource(context.TODO(), cl, "johnsmith", role)
			require.NoError(t, err)
			// check that the role was deleted
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &rbac.Role{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("multiple roles found by label and deletes successfully", func(t *testing.T) {
			role1 := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			role2 := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match-second",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := test.NewFakeClient(t, role1, role2)
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.Role{})
			require.NoError(t, err)
			// check that the roles were deleted
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &rbac.Role{})
			require.True(t, errors.IsNotFound(err))
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match-second", Namespace: UserSettingNS}, &rbac.Role{})
			require.True(t, errors.IsNotFound(err))
		})

		t.Run("Error is returned when error in deleting role", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting role")
			}
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.Role{})
			require.Error(t, err)
			require.Equal(t, "error in deleting role", err.Error())
		})
		t.Run("No Error is returned when no role not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.Role{})
			require.NoError(t, err)
		})

	})

	t.Run("Delete RoleBinding", func(t *testing.T) {
		t.Run("RoleBinding found by name and deleted", func(t *testing.T) {
			rb := &rbac.RoleBinding{
				TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-johnsmith",
					Namespace: UserSettingNS,
				},
			}
			cl := test.NewFakeClient(t, rb)

			err := deleteResource(context.Background(), cl, "johnsmith", rb)
			require.NoError(t, err)
			// check that the rolebinding doesn't exist anymore
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-johnsmith", Namespace: UserSettingNS}, &rbac.RoleBinding{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("RoleBinding found by label and deleted successfully", func(t *testing.T) {
			rb := &rbac.RoleBinding{
				TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := test.NewFakeClient(t, rb)
			err := deleteResource(context.TODO(), cl, "johnsmith", rb)
			require.NoError(t, err)
			// check that the rolebinding doesn't exist anymore
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &rbac.RoleBinding{})
			require.True(t, errors.IsNotFound(err))
		})
		t.Run("multiple RoleBindings found by label and deletes successfully", func(t *testing.T) {
			rb1 := &rbac.RoleBinding{
				TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			rb2 := &rbac.RoleBinding{
				TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-name-no-match-second",
					Namespace: UserSettingNS,
					Labels: map[string]string{
						ConsoleUserSettingsIdentifier: "true",
						ConsoleUserSettingsUID:        "johnsmith",
					},
				},
			}
			cl := test.NewFakeClient(t, rb1, rb2)
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.RoleBinding{})
			require.NoError(t, err)
			// check that the rolebindings don't exist anymore
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match", Namespace: UserSettingNS}, &rbac.RoleBinding{})
			require.True(t, errors.IsNotFound(err))
			err = cl.Get(context.TODO(), client.ObjectKey{Name: "user-settings-name-no-match-second", Namespace: UserSettingNS}, &rbac.RoleBinding{})
			require.True(t, errors.IsNotFound(err))
		})

		t.Run("Error is returned when error in deleting RoleBinding", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting RoleBinding")
			}
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.RoleBinding{})
			require.Error(t, err)
			require.Equal(t, "error in deleting RoleBinding", err.Error())
		})
		t.Run("No Error is returned when no RoleBinding not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			err := deleteResource(context.TODO(), cl, "johnsmith", &rbac.RoleBinding{})
			require.NoError(t, err)
		})
	})
}
