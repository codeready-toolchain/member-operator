package nstemplateset

import (
	"context"
	"fmt"
	"slices"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type clusterResourcesManager struct {
	*statusManager
}

// ensure ensures that the cluster resources exist.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
//
// NOTE: This method makes 1 important assumption: all the cluster-scoped resources of the NSTemplateSet are uniquely
// associated with it. I.e. the logic doesn't work correctly if 2 NSTemplateSets declare the very same cluster-scoped
// resource. It is assumed that if a cluster-scoped resource should be common to more than 1 nstemplateset, it should
// be deployed externally from Devsandbox, e.g. using ArgoCD.
func (r *clusterResourcesManager) ensure(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	logger := log.FromContext(ctx)
	userTierLogger := logger.WithValues("spacename", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	userTierCtx := log.IntoContext(ctx, userTierLogger)
	userTierLogger.Info("ensuring cluster resources")

	spacename := nsTmplSet.GetName()

	var oldTemplateRef, newTemplateRef string
	if nsTmplSet.Spec.ClusterResources != nil {
		newTemplateRef = nsTmplSet.Spec.ClusterResources.TemplateRef
	}
	if nsTmplSet.Status.ClusterResources != nil {
		oldTemplateRef = nsTmplSet.Status.ClusterResources.TemplateRef
	}

	if oldTemplateRef == newTemplateRef {
		return nil
	}

	var newTierTemplate *tierTemplate
	var oldTierTemplate *tierTemplate
	var err error
	if newTemplateRef != "" {
		newTierTemplate, err = getTierTemplate(ctx, r.GetHostClusterClient, newTemplateRef)
		if err != nil {
			return r.wrapErrorWithStatusUpdateForClusterResourceFailure(userTierCtx, nsTmplSet, err,
				"failed to retrieve the TierTemplate for the to-be-applied cluster resources with the name '%s'", newTemplateRef)
		}
	}
	if oldTemplateRef != "" {
		oldTierTemplate, err = getTierTemplate(ctx, r.GetHostClusterClient, oldTemplateRef)
		if err != nil {
			return r.wrapErrorWithStatusUpdateForClusterResourceFailure(userTierCtx, nsTmplSet, err,
				"failed to retrieve TierTemplate for the last-applied cluster resources with the name '%s'", oldTemplateRef)
		}
	}

	var newObjs, currentObjects []runtimeclient.Object
	if newTierTemplate != nil {
		newObjs, err = newTierTemplate.process(r.Scheme, map[string]string{SpaceName: spacename})
		if err != nil {
			return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
				"failed to process template for the to-be-applied cluster resources with the name '%s'", newTemplateRef)
		}
	}
	if oldTierTemplate != nil {
		currentObjects, err = oldTierTemplate.process(r.Scheme, map[string]string{SpaceName: spacename})
		if err != nil {
			return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
				"failed to process template for the last-applied cluster resources with the name '%s'", oldTemplateRef)
		}
	}

	statusChanged := false
	firstDeployment := nsTmplSet.Status.ClusterResources == nil || nsTmplSet.Status.ClusterResources.TemplateRef == ""
	var failureStatusReason statusUpdater
	if firstDeployment {
		failureStatusReason = r.setStatusClusterResourcesProvisionFailed
	} else {
		failureStatusReason = r.setStatusUpdateFailed
	}

	changeStatusIfNeeded := func() error {
		if !statusChanged {
			if firstDeployment {
				err = r.setStatusProvisioningIfNotUpdating(ctx, nsTmplSet)
			} else {
				err = r.setStatusUpdatingIfNotProvisioning(ctx, nsTmplSet)
			}
			if err != nil {
				return err
			}
			statusChanged = true
		}
		return nil
	}

	for _, newObj := range newObjs {
		if !shouldCreate(newObj, nsTmplSet) {
			continue
		}

		// by removing the new objects from the current objects, we are going to be left with the objects
		// that should be removed from the cluster after we're done with this loop
		for oldIdx, curObj := range currentObjects {
			if curObj.GetName() == newObj.GetName() && curObj.GetNamespace() == newObj.GetNamespace() && curObj.GetObjectKind().GroupVersionKind() == newObj.GetObjectKind().GroupVersionKind() {
				currentObjects = slices.Delete(currentObjects, oldIdx, oldIdx+1)
				break
			}
		}

		if err := changeStatusIfNeeded(); err != nil {
			return err
		}

		// create or update the resource
		if _, err = r.apply(ctx, nsTmplSet, newTierTemplate, newObj); err != nil {
			err := fmt.Errorf("failed to apply changes to the cluster resource %s, %s: %w", newObj.GetName(), newObj.GetObjectKind().GroupVersionKind().String(), err)
			return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, failureStatusReason, err, "failure while syncing cluster resources")
		}
	}

	// what we're left with here is the list of currently existing objects that are no longer present in the template.
	// we need to delete them
	for _, obj := range currentObjects {
		if err := changeStatusIfNeeded(); err != nil {
			return err
		}
		if err := r.Client.Delete(ctx, obj); err != nil {
			if !errors.IsNotFound(err) {
				err := fmt.Errorf("failed to delete the cluster resource %s, %s: %w", obj.GetName(), obj.GetObjectKind().GroupVersionKind().String(), err)
				return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, failureStatusReason, err, "failure while syncing cluster resources")
			}
		}
	}

	return nil
}

// apply creates or updates the given object with the set of toolchain labels. If the apply operation was successful, then it returns 'true, nil',
// but if there was an error then it returns 'false, error'.
func (r *clusterResourcesManager) apply(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate, object runtimeclient.Object) (bool, error) {
	labels := map[string]string{
		toolchainv1alpha1.SpaceLabelKey:       nsTmplSet.GetName(),
		toolchainv1alpha1.TypeLabelKey:        toolchainv1alpha1.ClusterResourcesTemplateType,
		toolchainv1alpha1.TemplateRefLabelKey: tierTemplate.templateRef,
		toolchainv1alpha1.TierLabelKey:        tierTemplate.tierName,
		toolchainv1alpha1.ProviderLabelKey:    toolchainv1alpha1.ProviderLabelValue,
	}
	// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
	// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	log.FromContext(ctx).Info("applying cluster resource", "object_name", object.GetObjectKind().GroupVersionKind().Kind+"/"+object.GetName())
	createdOrModified, err := r.ApplyToolchainObjects(ctx, []runtimeclient.Object{object}, labels)
	if err != nil {
		return false, errs.Wrapf(err, "failed to apply cluster resource")
	}
	return createdOrModified, nil
}

// delete deletes all cluster-scoped resources referenced by the nstemplateset.
func (r *clusterResourcesManager) delete(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	if nsTmplSet.Status.ClusterResources == nil {
		return nil
	}

	currentTierTemplate, err := getTierTemplate(ctx, r.GetHostClusterClient, nsTmplSet.Status.ClusterResources.TemplateRef)
	if err != nil {
		return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
			"failed to read the existing cluster resources")
	}
	currentObjects, err := currentTierTemplate.process(r.Scheme, map[string]string{SpaceName: nsTmplSet.Name})
	if err != nil {
		return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
			"failed to list existing cluster resources")
	}

	for _, toDelete := range currentObjects {
		if err := r.Client.Get(ctx, types.NamespacedName{Name: toDelete.GetName()}, toDelete); err != nil && !errors.IsNotFound(err) {
			return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusTerminatingFailed, err,
				"failed to get the cluster resource '%s' (GVK '%s') while deleting cluster resources", toDelete.GetName(), toDelete.GetObjectKind().GroupVersionKind())
		}
		// ignore cluster resource that are already flagged for deletion
		if errors.IsNotFound(err) || util.IsBeingDeleted(toDelete) {
			continue
		}

		log.FromContext(ctx).Info("deleting cluster resource", "name", toDelete.GetName(), "kind", toDelete.GetObjectKind().GroupVersionKind().Kind)
		if err := r.Client.Delete(ctx, toDelete); err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", toDelete.GetName())
		}
	}
	return nil
}
