package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

type clusterResourcesManager struct {
	*statusManager
}

// ensure ensures that the cluster resources exists.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
func (r *clusterResourcesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	logger.Info("ensuring cluster resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	username := nsTmplSet.GetName()
	newObjs := make([]runtime.RawExtension, 0)
	var tierTemplate *tierTemplate
	var err error
	if nsTmplSet.Spec.ClusterResources != nil {
		tierTemplate, err = getTierTemplate(r.getHostCluster, nsTmplSet.Spec.ClusterResources.TemplateRef)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err,
				"failed to retrieve TierTemplate for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
		}
		newObjs, err = tierTemplate.process(r.scheme, username)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err,
				"failed to process template for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
		}
	}

	// let's look for existing cluster resource quotas to determine the current tier
	crqs := quotav1.ClusterResourceQuotaList{}
	if err := r.client.List(context.TODO(), &crqs, listByOwnerLabel(username)); err != nil {
		logger.Error(err, "failed to list existing cluster resource quotas")
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "failed to list existing cluster resource quotas")
	} else if len(crqs.Items) > 0 {
		// only if necessary
		crqMeta, err := meta.Accessor(&(crqs.Items[0]))
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "failed to get meta info from object %v", crqs.Items[0])
		}
		if currentTemplateRef, exists := crqMeta.GetLabels()[toolchainv1alpha1.TemplateRefLabelKey]; exists && (nsTmplSet.Spec.ClusterResources == nil || currentTemplateRef != nsTmplSet.Spec.ClusterResources.TemplateRef) {

			if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
				return false, err
			}

			currentTierTemplate, err := getTierTemplate(r.getHostCluster, currentTemplateRef)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err,
					"failed to retrieve TierTemplate for the cluster resources with the name '%s'", currentTemplateRef)
			}
			currentObjs, err := currentTierTemplate.process(r.scheme, username)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err,
					"failed to process template for the cluster resources with the name '%s'", currentTemplateRef)
			}
			if deleted, err := deleteRedundantObjects(logger, r.client, true, currentObjs, newObjs); err != nil {
				logger.Error(err, "failed to delete redundant cluster resources")
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant cluster resources")
			} else if deleted {
				return true, nil // something changed
			}
		}
	}
	if len(newObjs) == 0 {
		logger.Info("no cluster resources to create or update")
		return false, nil
	}

	var labels = map[string]string{
		toolchainv1alpha1.OwnerLabelKey:       nsTmplSet.GetName(),
		toolchainv1alpha1.TypeLabelKey:        toolchainv1alpha1.ClusterResourcesTemplateType,
		toolchainv1alpha1.TemplateRefLabelKey: tierTemplate.templateRef,
		toolchainv1alpha1.TierLabelKey:        tierTemplate.tierName,
		toolchainv1alpha1.ProviderLabelKey:    toolchainv1alpha1.ProviderLabelValue,
	}
	// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
	// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	for _, obj := range newObjs {
		logger.Info("applying cluster resources object", "obj", obj.Object.GetObjectKind(), "username", username)
		if createdOrUpdated, err := applycl.NewApplyClient(r.client, r.scheme).Apply([]runtime.RawExtension{obj}, labels); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "failed to create cluster resources")
		} else if createdOrUpdated {
			namespaces, err := fetchNamespaces(r.client, username)
			if err != nil {
				return true, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err, "failed to list namespace with label owner '%s'", username)
			}
			if len(namespaces) == 0 {
				logger.Info("provisioned cluster resource")
				return true, r.setStatusProvisioningIfNotUpdating(nsTmplSet)
			}
			logger.Info("updated cluster resource")
			return true, r.setStatusUpdatingIfNotProvisioning(nsTmplSet)
		}
	}
	logger.Info("cluster resources already provisioned")
	return false, nil
}

// delete deletes cluster scoped resources taken the ClusterResources template. The method deletes only one resource in one call
// and returns information if any resource was deleted or not. The cases are described below:
//
// If some resource that should be deleted is found, then it deletes it and returns 'true,nil'. If there is no resource to be deleted
// which means that everything was deleted previously, then it returns 'false,nil'. In case of any error it returns 'false,error'.
func (r *clusterResourcesManager) delete(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	if nsTmplSet.Spec.ClusterResources == nil {
		return false, nil
	}
	username := nsTmplSet.Name
	tierTemplate, err := getTierTemplate(r.getHostCluster, nsTmplSet.Spec.ClusterResources.TemplateRef)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err,
			"failed to retrieve TierTemplate for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
	}
	objs, err := tierTemplate.process(r.scheme, username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err,
			"failed to process template for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
	}
	logger.Info("listed cluster resources to delete", "count", len(objs))
	for _, obj := range objs {
		objectToDelete := obj.Object
		objMeta, err := meta.Accessor(objectToDelete)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource of kind '%s'", objectToDelete.GetObjectKind())
		}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: objMeta.GetName()}, objectToDelete); err != nil && !errors.IsNotFound(err) {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err,
				"failed to get current object '%s' while deleting cluster resource of kind '%s'", objMeta.GetName(), objectToDelete.GetObjectKind())
		}
		// ignore cluster resource that are already flagged for deletion
		if errors.IsNotFound(err) || objMeta.GetDeletionTimestamp() != nil {
			continue
		}
		logger.Info("deleting cluster resource", "name", objMeta.GetName())
		err = r.client.Delete(context.TODO(), objectToDelete)
		if err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", objMeta.GetName())
		}
		// stop there for now. Will reconcile again for the next cluster resource (if any exists)
		return true, nil
	}
	return false, nil
}
