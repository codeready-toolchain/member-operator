package nstemplateset

import (
	"context"
	"fmt"
	"slices"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"

	commonclient "github.com/codeready-toolchain/toolchain-common/pkg/client"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
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
	logger := log.FromContext(ctx, "spacename", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	logger.Info("ensuring cluster resources")
	ctx = log.IntoContext(ctx, logger)

	oldTemplateRef, newTemplateRef, changed := getOldAndNewTemplateRefsIfChanged(nsTmplSet)
	if !changed {
		return nil
	}

	newTierTemplate, newObjs, err := r.processTierTemplate(ctx, nsTmplSet.Spec.ClusterResources, nsTmplSet.Name)
	if err != nil {
		return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
			"failed to process the template for the to-be-applied cluster resources with the name '%s'", newTemplateRef)
	}

	_, curObjs, err := r.processTierTemplate(ctx, nsTmplSet.Status.ClusterResources, nsTmplSet.Name)
	if err != nil {
		return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
			"failed to process the template for the last-applied cluster resources with the name '%s'", oldTemplateRef)
	}

	objectApplier := newObjectApplier(r, nsTmplSet, curObjs, newTierTemplate)

	for _, newObj := range newObjs {
		if err = objectApplier.Apply(ctx, newObj); err != nil {
			return err
		}
	}

	return objectApplier.Cleanup(ctx)
}

func getOldAndNewTemplateRefsIfChanged(nstt *toolchainv1alpha1.NSTemplateSet) (oldTemplateRef string, newTemplateRef string, changed bool) {
	if nstt.Spec.ClusterResources != nil {
		newTemplateRef = nstt.Spec.ClusterResources.TemplateRef
	}
	if nstt.Status.ClusterResources != nil {
		oldTemplateRef = nstt.Status.ClusterResources.TemplateRef
	}

	changed = oldTemplateRef != newTemplateRef || featuresChanged(nstt)

	return oldTemplateRef, newTemplateRef, changed
}

func (r *clusterResourcesManager) processTierTemplate(ctx context.Context, clusterResources *toolchainv1alpha1.NSTemplateSetClusterResources, spacename string) (*tierTemplate, []runtimeclient.Object, error) {
	if clusterResources == nil {
		return nil, nil, nil
	}

	var tierTemplate *tierTemplate
	var objs []runtimeclient.Object
	if clusterResources.TemplateRef != "" {
		var err error
		tierTemplate, err = getTierTemplate(ctx, r.GetHostClusterClient, clusterResources.TemplateRef)
		if err != nil {
			return nil, nil, err
		}
		objs, err = tierTemplate.process(r.Scheme, map[string]string{SpaceName: spacename})
		if err != nil {
			return nil, nil, err
		}
	}

	return tierTemplate, objs, nil
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

	_, currentObjects, err := r.processTierTemplate(ctx, nsTmplSet.Status.ClusterResources, nsTmplSet.Name)
	if err != nil {
		return r.wrapErrorWithStatusUpdateForClusterResourceFailure(ctx, nsTmplSet, err,
			"failed to process the existing cluster resources")
	}

	for _, toDelete := range currentObjects {
		var err error
		if err = r.Client.Get(ctx, runtimeclient.ObjectKeyFromObject(toDelete), toDelete); err != nil && !errors.IsNotFound(err) {
			return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusTerminatingFailed, err,
				"failed to get the cluster resource '%s' (GVK '%s') while deleting cluster resources", toDelete.GetName(), toDelete.GetObjectKind().GroupVersionKind())
		}
		// ignore cluster resource that are already flagged for deletion
		if errors.IsNotFound(err) || util.IsBeingDeleted(toDelete) {
			continue
		}

		log.FromContext(ctx).Info("deleting cluster resource", "name", toDelete.GetName(), "kind", toDelete.GetObjectKind().GroupVersionKind().Kind)
		if err = r.Client.Delete(ctx, toDelete); err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return r.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", toDelete.GetName())
		}
	}
	return nil
}

// objectApplier is a helper to the clusterResourcesManager.ensure() method.
// A new instance can be obtained using the newObjectApplier() function.
//
// It takes in the current state of the NSTemplateSet and then processes one new object to apply
// to the cluster at a time, updating the internal bookkeeping to be able to Cleanup() the old objects
// after all the new objects have been applied.
type objectApplier struct {
	r                   *clusterResourcesManager
	statusChanged       bool
	firstDeployment     bool
	failureStatusReason statusUpdater
	nstt                *toolchainv1alpha1.NSTemplateSet
	currentObjects      []runtimeclient.Object
	newTierTemplate     *tierTemplate
}

func newObjectApplier(r *clusterResourcesManager, nstt *toolchainv1alpha1.NSTemplateSet, currentObjects []runtimeclient.Object, newTierTemplate *tierTemplate) *objectApplier {
	// if there's no clusterresources templateref mentioned in the tiertemplate's status, it was never deployed before.
	firstDeployment := nstt.Status.ClusterResources == nil || nstt.Status.ClusterResources.TemplateRef == ""
	var failureStatusReason statusUpdater

	if firstDeployment {
		failureStatusReason = r.setStatusClusterResourcesProvisionFailed
	} else {
		failureStatusReason = r.setStatusUpdateFailed
	}

	return &objectApplier{
		r:                   r,
		statusChanged:       false,
		firstDeployment:     firstDeployment,
		failureStatusReason: failureStatusReason,
		nstt:                nstt,
		currentObjects:      currentObjects,
		newTierTemplate:     newTierTemplate,
	}
}

func (oa *objectApplier) changeStatusIfNeeded(ctx context.Context) error {
	if !oa.statusChanged {
		var err error
		if oa.firstDeployment {
			err = oa.r.setStatusProvisioningIfNotUpdating(ctx, oa.nstt)
		} else {
			err = oa.r.setStatusUpdatingIfNotProvisioning(ctx, oa.nstt)
		}
		if err != nil {
			return err
		}
		oa.statusChanged = true
	}
	return nil
}

func (oa *objectApplier) Apply(ctx context.Context, obj runtimeclient.Object) error {
	if !shouldCreate(obj, oa.nstt) {
		return nil
	}

	// by removing the new objects from the current objects, we are going to be left with the objects
	// that should be removed from the cluster after we're done with this loop
	for oldIdx, curObj := range oa.currentObjects {
		if commonclient.SameGVKandName(curObj, obj) {
			oa.currentObjects = slices.Delete(oa.currentObjects, oldIdx, oldIdx+1)
			break
		}
	}

	if err := oa.changeStatusIfNeeded(ctx); err != nil {
		return err
	}

	// create or update the resource
	if _, err := oa.r.apply(ctx, oa.nstt, oa.newTierTemplate, obj); err != nil {
		err := fmt.Errorf("failed to apply changes to the cluster resource %s, %s: %w", obj.GetName(), obj.GetObjectKind().GroupVersionKind().String(), err)
		return oa.r.wrapErrorWithStatusUpdate(ctx, oa.nstt, oa.failureStatusReason, err, "failure while syncing cluster resources")
	}

	return nil
}

func (oa *objectApplier) Cleanup(ctx context.Context) error {
	// what we're left with here is the list of currently existing objects that are no longer present in the template.
	// we need to delete them
	if len(oa.currentObjects) > 0 {
		if err := oa.changeStatusIfNeeded(ctx); err != nil {
			return err
		}
	}

	if err := deleteObsoleteObjects(ctx, oa.r.Client, oa.currentObjects, nil); err != nil {
		return oa.r.wrapErrorWithStatusUpdate(ctx, oa.nstt, oa.failureStatusReason, err, "failure while syncing cluster resources")
	}

	return nil
}
