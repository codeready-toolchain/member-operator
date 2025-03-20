package nstemplateset

import (
	"context"
	"sort"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/utils"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type statusManager struct {
	*APIClient
}

// error handling methods
type statusUpdater func(context.Context, *toolchainv1alpha1.NSTemplateSet, string) error

func (r *statusManager) wrapErrorWithStatusUpdateForClusterResourceFailure(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, err error, format string, args ...interface{}) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdatingReason || readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdateFailedReason {
		return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusUpdateFailed, err, format, args...)
	}
	return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, format, args...)
}

func (r *statusManager) wrapErrorWithStatusUpdateForSpaceRolesFailure(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, err error, format string, args ...interface{}) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdatingReason || readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdateFailedReason {
		return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusUpdateFailed, err, format, args...)
	}
	return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusSpaceRolesProvisionFailed, err, format, args...)
}

func (r *statusManager) wrapErrorWithStatusUpdate(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, updateStatus statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := updateStatus(ctx, nsTmplSet, err.Error()); err != nil {
		log.FromContext(ctx).Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *statusManager) updateStatusConditions(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	nsTmplSet.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(nsTmplSet.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.Client.Status().Update(ctx, nsTmplSet)
}

func (r *statusManager) updateStatusProvisionedNamespaces(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, namespaces []corev1.Namespace) error {
	if len(namespaces) == 0 {
		// no namespaces to set
		return nil
	}

	var provisionedNamespaces []toolchainv1alpha1.SpaceNamespace
	for _, ns := range namespaces {
		provisionedNamespaces = append(provisionedNamespaces, toolchainv1alpha1.SpaceNamespace{
			Name: ns.Name,
		})
	}
	// todo update logic that sets the type of namespace
	// for now we just set "default" to the first namespace in alphabetical order
	sort.Slice(provisionedNamespaces, func(i, j int) bool {
		return provisionedNamespaces[i].Name < provisionedNamespaces[j].Name
	})
	provisionedNamespaces[0].Type = toolchainv1alpha1.NamespaceTypeDefault

	nsTmplSet.Status.ProvisionedNamespaces = provisionedNamespaces
	return r.Client.Status().Update(ctx, nsTmplSet)
}

func (r *statusManager) setStatusReady(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	// update also all the status revisions
	updateRevisions(nsTmplSet)
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.NSTemplateSetProvisionedReason,
		})
}

func updateRevisions(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
	nsTmplSet.Status.ClusterResources = nsTmplSet.Spec.ClusterResources
	featureAnnotation, featureAnnotationFound := nsTmplSet.Annotations[toolchainv1alpha1.FeatureToggleNameAnnotationKey]
	if featureAnnotationFound {
		// save the feature toggles into the status
		nsTmplSet.Status.FeatureToggles = utils.SplitCommaSeparatedList(featureAnnotation)
	}
	nsTmplSet.Status.Namespaces = nsTmplSet.Spec.Namespaces
	nsTmplSet.Status.SpaceRoles = nsTmplSet.Spec.SpaceRoles
}

func (r *statusManager) updateStatusClusterResourcesRevisions(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	nsTmplSet.Status.ClusterResources = nsTmplSet.Spec.ClusterResources
	featureAnnotation, featureAnnotationFound := nsTmplSet.Annotations[toolchainv1alpha1.FeatureToggleNameAnnotationKey]
	if featureAnnotationFound {
		// save the feature toggles into the status
		nsTmplSet.Status.FeatureToggles = utils.SplitCommaSeparatedList(featureAnnotation)
	}
	return r.Client.Status().Update(ctx, nsTmplSet)
}

func (r *statusManager) updateStatusNamespacesRevisions(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	nsTmplSet.Status.Namespaces = nsTmplSet.Spec.Namespaces
	return r.Client.Status().Update(ctx, nsTmplSet)
}

func (r *statusManager) updateStatusSpaceRolesRevisions(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	nsTmplSet.Status.SpaceRoles = nsTmplSet.Spec.SpaceRoles
	return r.Client.Status().Update(ctx, nsTmplSet)
}

func (r *statusManager) setStatusProvisioningIfNotUpdating(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetUpdatingReason {
		return nil
	}
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetProvisioningReason,
		})
}

func (r *statusManager) setStatusProvisionFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionReason,
			Message: message,
		})
}

func (r *statusManager) setStatusNamespaceProvisionFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
			Message: message,
		})
}

func (r *statusManager) setStatusClusterResourcesProvisionFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionClusterResourcesReason,
			Message: message,
		})
}

func (r *statusManager) setStatusSpaceRolesProvisionFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionSpaceRolesReason,
			Message: message,
		})
}

func (r *statusManager) setStatusTerminating(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
		})
}

func (r *statusManager) setStatusUpdatingIfNotProvisioning(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	readyCondition, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if found && readyCondition.Reason == toolchainv1alpha1.NSTemplateSetProvisioningReason {
		return nil
	}
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetUpdatingReason,
		})
}

func (r *statusManager) setStatusUpdateFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUpdateFailedReason,
			Message: message,
		})
}

func (r *statusManager) setStatusTerminatingFailed(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		ctx,
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetTerminatingFailedReason,
			Message: message,
		})
}
