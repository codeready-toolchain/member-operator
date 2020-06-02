package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/go-logr/logr"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

type statusManager struct {
	*apiClient
}

// error handling methods
type statusUpdater func(*toolchainv1alpha1.NSTemplateSet, string) error

func (r *statusManager) wrapErrorWithStatusUpdateForClusterResourceFailure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, err error, format string, args ...interface{}) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdatingReason || readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdateFailedReason {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, format, args...)
	} else {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, format, args...)
	}
}

func (r *statusManager) wrapErrorWithStatusUpdate(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, updateStatus statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := updateStatus(nsTmplSet, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *statusManager) updateStatusConditions(nsTmplSet *toolchainv1alpha1.NSTemplateSet, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	nsTmplSet.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(nsTmplSet.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), nsTmplSet)
}

func (r *statusManager) setStatusReady(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.NSTemplateSetProvisionedReason,
		})
}

func (r *statusManager) setStatusProvisioningIfNotUpdating(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdatingReason {
		return nil
	}
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetProvisioningReason,
		})
}

func (r *statusManager) setStatusProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionReason,
			Message: message,
		})
}

func (r *statusManager) setStatusNamespaceProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
			Message: message,
		})
}

func (r *statusManager) setStatusClusterResourcesProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionClusterResourcesReason,
			Message: message,
		})
}

func (r *statusManager) setStatusTerminating(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
		})
}

func (r *statusManager) setStatusUpdatingIfNotProvisioning(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetProvisioningReason {
		return nil
	}
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetUpdatingReason,
		})
}

func (r *statusManager) setStatusUpdateFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUpdateFailedReason,
			Message: message,
		})
}

func (r *statusManager) setStatusTerminatingFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetTerminatingFailedReason,
			Message: message,
		})
}
