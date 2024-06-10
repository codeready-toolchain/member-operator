package useraccount

import (
	"context"
	"fmt"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestDeleteConsoleSettingObjects(t *testing.T) {
	t.Run("Object found by name and deleted", func(t *testing.T) {
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
	t.Run("Object found by label and deletes successfully", func(t *testing.T) {
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
			TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-settings-name-no-match-noise",
				Namespace: UserSettingNS,
				Labels: map[string]string{
					ConsoleUserSettingsIdentifier: "true",
				},
			},
		}
		cl := test.NewFakeClient(t, cm, noiseObject)
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
		require.NoError(t, err)
		// check that the configmap doesn't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "johnsmith", &corev1.ConfigMap{})
		// check that the noise object still exists
		AssertObject(t, cl, UserSettingNS, "user-settings-name-no-match-noise", noiseObject, func() {
			assert.Equal(t, map[string]string{ConsoleUserSettingsIdentifier: "true"}, noiseObject.Labels)
		})
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
		cl := test.NewFakeClient(t, cm1, cm2)
		err := deleteResource(context.TODO(), cl, "johnsmith", &corev1.ConfigMap{})
		require.NoError(t, err)
		// check that the configmaps don't exist anymore
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-name-no-match", &corev1.ConfigMap{})
		AssertObjectNotFound(t, cl, UserSettingNS, "user-settings-name-no-match-second", &corev1.ConfigMap{})
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

}
