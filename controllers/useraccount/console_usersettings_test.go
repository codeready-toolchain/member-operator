package useraccount

import (
	"context"
	"fmt"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestGetConsoleUserSettingObjectByName(t *testing.T) {
	ctx := context.Background()
	t.Run("Get ConfigMap", func(t *testing.T) {
		t.Run("Configmap found by name returns expected object", func(t *testing.T) {
			cm := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(cm).Build()
			name := "test-configmap"
			get_cm := &corev1.ConfigMap{TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, get_cm)
			require.NoError(t, err)
			require.NotEmpty(t, result)
			require.Equal(t, name, result.GetName())
		})
		t.Run("Configmap not found by name returns nil", func(t *testing.T) {
			cl := fake.NewClientBuilder().WithObjects().Build()
			name := "non-existent-configmap"
			cm := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, cm)
			require.NoError(t, err)
			require.Nil(t, result)
		})
		t.Run("error in getting configmap", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting configmap")
			}
			name := "error-configmap"
			rb := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, rb)
			require.Error(t, err)
			require.Equal(t, "error in getting configmap", err.Error())
			require.Nil(t, result)
		})
	})

	t.Run("Get Role", func(t *testing.T) {
		t.Run("Role found by name returns expected object", func(t *testing.T) {
			role := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-role",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(role).Build()
			name := "test-role"
			get_role := &rbac.Role{TypeMeta: metav1.TypeMeta{Kind: "Role"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, get_role)
			require.NoError(t, err)
			require.NotEmpty(t, result)
			require.Equal(t, name, result.GetName())
		})
		t.Run("Role not found by name returns nil", func(t *testing.T) {
			cl := fake.NewClientBuilder().WithObjects().Build()
			name := "non-existent-role"
			role := &rbac.Role{TypeMeta: metav1.TypeMeta{Kind: "Role"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, role)
			require.NoError(t, err)
			require.Nil(t, result)
		})
		t.Run("error in getting Role", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting Role")
			}
			name := "error-role"
			role := &rbac.Role{TypeMeta: metav1.TypeMeta{Kind: "Role"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, role)
			require.Error(t, err)
			require.Equal(t, "error in getting Role", err.Error())
			require.Nil(t, result)
		})
	})

	t.Run("Get RoleBinding", func(t *testing.T) {
		t.Run("RoleBinding found by name returns expected object", func(t *testing.T) {
			rb := &rbac.RoleBinding{
				TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rolebinding",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(rb).Build()
			name := "test-rolebinding"
			get_rb := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, get_rb)
			require.NoError(t, err)
			require.NotEmpty(t, result)
			require.Equal(t, name, result.GetName())
		})
		t.Run("RoleBinding not found by name returns nil", func(t *testing.T) {
			cl := fake.NewClientBuilder().WithObjects().Build()
			name := "non-existent-rolebinding"
			rb := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, rb)
			require.NoError(t, err)
			require.Nil(t, result)
		})
		t.Run("error in getting RoleBinding", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting RoleBinding")
			}
			name := "error-rolebinding"
			rb := &rbac.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"}}

			result, err := getConsoleUserSettingObjectByName(ctx, cl, name, rb)
			require.Error(t, err)
			require.Equal(t, "error in getting RoleBinding", err.Error())
			require.Nil(t, result)
		})
	})

	t.Run("Getting any other object returns error", func(t *testing.T) {
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: UserSettingNS,
			},
		}
		cl := fake.NewClientBuilder().WithObjects(secret).Build()
		name := "test-secret"
		get_obj := &corev1.Secret{TypeMeta: metav1.TypeMeta{Kind: "Secret"}}

		result, err := getConsoleUserSettingObjectByName(ctx, cl, name, get_obj)
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, "object type Secret is not a console setting supported object", err.Error())
	})
}

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

			deleted, err := deleteConfigMap(ctx, cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
		})
		t.Run("Error is returned when error in getting configmap", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting configmap")
			}
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in getting configmap", err.Error())
			require.False(t, deleted)
		})
		t.Run("Error is returned when error in deleting configmap", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting configmap")
			}
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in deleting configmap", err.Error())
			require.False(t, deleted)
		})
		t.Run("No Error is returned when no configmap not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.False(t, deleted)
		})
		t.Run("Error is returned when there is an error in listing configmaps", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("error in listing configmaps")
			}
			deleted, err := deleteConfigMap(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in listing configmaps", err.Error())
			require.False(t, deleted)
		})
	})

	t.Run("Delete Role", func(t *testing.T) {
		t.Run("Role found by name and deleted", func(t *testing.T) {
			ctx := context.Background()
			rb := &rbac.Role{
				TypeMeta: metav1.TypeMeta{Kind: "Role"},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-settings-johnsmith",
					Namespace: UserSettingNS,
				},
			}
			cl := fake.NewClientBuilder().WithObjects(rb).Build()

			deleted, err := deleteRole(ctx, cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
		})
		t.Run("Error is returned when error in getting role", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting role")
			}
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in getting role", err.Error())
			require.False(t, deleted)
		})
		t.Run("Error is returned when error in deleting role", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting role")
			}
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in deleting role", err.Error())
			require.False(t, deleted)
		})
		t.Run("No Error is returned when no role not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.False(t, deleted)
		})
		t.Run("Error is returned when there is an error in listing roles", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("error in listing roles")
			}
			deleted, err := deleteRole(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in listing roles", err.Error())
			require.False(t, deleted)
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

			deleted, err := deleteRoleBinding(context.Background(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
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
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.True(t, deleted)
		})
		t.Run("Error is returned when error in getting RoleBinding", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return fmt.Errorf("error in getting RoleBinding")
			}
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in getting RoleBinding", err.Error())
			require.False(t, deleted)
		})
		t.Run("Error is returned when error in deleting RoleBinding", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			}
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("error in deleting RoleBinding")
			}
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in deleting RoleBinding", err.Error())
			require.False(t, deleted)
		})
		t.Run("No Error is returned when no RoleBinding not found", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.NoError(t, err)
			require.False(t, deleted)
		})
		t.Run("Error is returned when there is an error in listing RoleBindings", func(t *testing.T) {
			cl := test.NewFakeClient(t)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("error in listing RoleBindings")
			}
			deleted, err := deleteRoleBinding(context.TODO(), cl, "johnsmith")
			require.Error(t, err)
			require.Equal(t, "error in listing RoleBindings", err.Error())
			require.False(t, deleted)
		})
	})
}
