package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestUpdateStatus(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("status updated", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)
		condition := toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}

		// when
		err := statusManager.updateStatusConditions(nsTmplSet, condition)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(condition)
	})

	t.Run("update provisioned namespaces", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic")
		namespaces := []corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: username + "-stage"}},
			{ObjectMeta: metav1.ObjectMeta{Name: username + "-dev"}},
		}
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.updateStatusProvisionedNamespaces(nsTmplSet, namespaces)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasProvisionedNamespaces([]toolchainv1alpha1.SpaceNamespace{
				{
					Name: username + "-dev",
					Type: "default", // check that default type is added to first NS in alphabetical order
				},
				{
					Name: username + "-stage",
					Type: "", // other namespaces do not have type for now...
				},
			}...)
	})

	t.Run("no provisioned namespaces", func(t *testing.T) {
		// given
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}}
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withConditions(conditions...))
		namespaces := []corev1.Namespace{} // empty list of user namespaces are given for some weired issue
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.updateStatusProvisionedNamespaces(nsTmplSet, namespaces)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasProvisionedNamespaces([]toolchainv1alpha1.SpaceNamespace(nil)...) // provisioned namespaces list is nil
	})

	t.Run("status not updated because not changed", func(t *testing.T) {
		// given
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
		}}
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(conditions...))
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.updateStatusConditions(nsTmplSet, conditions...)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(conditions...)
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
		statusManager, _ := prepareStatusManager(t, nsTmplSet)
		log := logf.Log.WithName("test")

		t.Run("status_updated", func(t *testing.T) {
			// given
			statusUpdater := func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
				assert.Equal(t, "oopsy woopsy", message)
				return nil
			}

			// when
			err := statusManager.wrapErrorWithStatusUpdate(log, nsTmplSet, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create namespace")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create namespace: oopsy woopsy", err.Error())
		})

		t.Run("status update failed", func(t *testing.T) {
			// given
			statusUpdater := func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
				return errors.New("unable to update status")
			}

			// when
			err := statusManager.wrapErrorWithStatusUpdate(log, nsTmplSet, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create namespace")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create namespace: oopsy woopsy", err.Error())
		})
	})

	t.Run("status update failures", func(t *testing.T) {

		t.Run("failed to update status during deletion", func(t *testing.T) {
			// given an NSTemplateSet resource which is being deleted and whose finalizer was not removed yet
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withDeletionTs(), withClusterResources("abcde11"), withNamespaces("abcde11", "dev", "code"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return fmt.Errorf("status update mock error")
			}
			// when a reconcile loop is triggered
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to set status to 'ready=false/reason=terminating' on NSTemplateSet: status update mock error", err.Error())
			AssertThatNSTemplateSet(t, namespaceName, username, r.Client).
				HasFinalizer(). // finalizer was not added and nothing else was done
				HasConditions() // no condition was set to status update error
		})
	})

	t.Run("don't set to provisioning if is set to updating", func(t *testing.T) {
		// given
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetUpdatingReason,
		}}
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(conditions...))
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.setStatusProvisioningIfNotUpdating(nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(conditions...)
	})

	t.Run("don't set to updating if is set to provisioning", func(t *testing.T) {
		// given
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetProvisioningReason,
		}}
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(conditions...))
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.setStatusUpdatingIfNotProvisioning(nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(conditions...)
	})
}
func TestUpdateStatusToProvisionedWhenPreviouslyWasSetToFailed(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)
	failed := toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
		Message: "Operation cannot be fulfilled on namespaces bla bla bla",
	}
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("when status is set to false with message, then next update to true should remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(failed))
		statusManager, fakeClient := prepareStatusManager(t, nsTmplSet)

		// when
		err := statusManager.setStatusReady(nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
	})

	t.Run("when status is set to false with message, then next successful reconcile should update it to true and remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withConditions(failed))
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", username, "stage", withTemplateRefUsingRevision("abcde11"))
		devRole := newRole(devNS.Name, "exec-pods", username)
		devRb1 := newRoleBinding(devNS.Name, "crtadmin-pods", username)
		devRb2 := newRoleBinding(devNS.Name, "crtadmin-view", username)
		stageRole := newRole(stageNS.Name, "exec-pods", username)
		stageRb1 := newRoleBinding(stageNS.Name, "crtadmin-pods", username)
		stageRb2 := newRoleBinding(stageNS.Name, "crtadmin-view", username)
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, stageNS, devRole, devRb1, devRb2, stageRole, stageRb1, stageRb2)

		// when
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
	})
}
