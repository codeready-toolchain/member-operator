package useraccount

import (
	"context"
	"fmt"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
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
	t.Run("Object found by label and deletes successfully", func(t *testing.T) {
		// given
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
		// create a noise objects where the labels don't match
		noiseObject := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-name-no-match-noise",
				Namespace: UserSettingNS,
				Labels: map[string]string{
					ConsoleUserSettingsIdentifier: "true",
				},
			},
		}
		cl := test.NewFakeClient(t, cm, noiseObject)

		// when
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})

		// then
		require.NoError(t, err)
		// check that the configmap doesn't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "johnsmith", &corev1.ConfigMap{})
		// check that the noise object still exists
		retrievedNoise := &corev1.ConfigMap{}
		AssertObject(t, cl, UserSettingNS, "user-settings-name-no-match-noise", retrievedNoise, func() {
			assert.Equal(t, noiseObject, retrievedNoise)
		})
	})
	t.Run("multiple objects found by label and deletes successfully", func(t *testing.T) {
		// given
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
		cl := test.NewFakeClient(t, cm1, cm2)

		// when
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})

		// then
		require.NoError(t, err)
		// check that the configmaps don't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-name-no-match", &corev1.ConfigMap{})
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-name-no-match-second", &corev1.ConfigMap{})
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
