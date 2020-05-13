package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type clusterResourcesManager struct {
	*statusManager
}

// mainClusterResource represents a resource type that should be present in all templates containing cluster resources
// "main-cluster-resource-type" should be watched by NSTempalateSet controller which means that every change of the
// resources of that type triggers a new reconcile
type mainClusterResource struct {
	gvk                   schema.GroupVersionKind
	object                runtime.Object
	listExistingResources getResourceList
}

// getResourceList returns a list of objects representing existing resources for the given user
type getResourceList func(cl client.Client, username string) ([]runtime.Object, error)

// mainClusterResources is a list that contains definitions for all "main-cluster-resource" types
var mainClusterResources = []mainClusterResource{
	{
		gvk:    quotav1.GroupVersion.WithKind("ClusterResourceQuota"),
		object: &quotav1.ClusterResourceQuota{},
		listExistingResources: func(cl client.Client, username string) ([]runtime.Object, error) {
			quotaList := &quotav1.ClusterResourceQuotaList{}
			if err := cl.List(context.TODO(), quotaList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]runtime.Object, len(quotaList.Items))
			for index, item := range quotaList.Items {
				list[index] = &item
			}
			return list, nil
		},
	},
}

// mainClusterResourcesSyncResult is a result of synchronization of existing and required resources that are of the "main-cluster-resource" types
type mainClusterResourcesSyncResult struct {
	toCreateOrUpdate       *runtime.RawExtension
	anyDeleted             bool
	currentTierToBeChanged string
}

var noResult = mainClusterResourcesSyncResult{}

// ensure ensures that the cluster resources exists.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
func (r *clusterResourcesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {

	syncResult, err := r.syncMainClusterResources(logger, nsTmplSet)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err, "unable to provision or update main cluster resources")
	}
	if syncResult.toCreateOrUpdate != nil || syncResult.anyDeleted {
		if err := r.ensureNotMainClusterResources(logger, syncResult.currentTierToBeChanged, nsTmplSet); err != nil {
			if syncResult.currentTierToBeChanged != "" {
				return syncResult.anyDeleted, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "unable to update cluster resources")
			}
			return syncResult.anyDeleted, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "unable to provision or update cluster resources")
		}
		if syncResult.anyDeleted {
			return true, nil
		}

		labels := clusterResourceLabels(nsTmplSet.GetName(), nsTmplSet.Spec.ClusterResources.Revision, nsTmplSet.Spec.TierName)
		// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
		// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
		// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
		// see https://issues.redhat.com/browse/CRT-429

		logger.Info("applying main cluster resource", "gvk", syncResult.toCreateOrUpdate.Object.GetObjectKind().GroupVersionKind())
		if _, err := applycl.NewApplyClient(r.client, r.scheme).Apply([]runtime.RawExtension{*syncResult.toCreateOrUpdate}, labels); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err,
				"failed to apply main cluster resource of type '%v'", syncResult.toCreateOrUpdate.Object.GetObjectKind().GroupVersionKind())
		}
		return true, nil
	}
	logger.Info("cluster resources already provisioned")
	return false, nil
}

// syncMainClusterResources takes the objects that are of the "main-cluster-resource" types from the ClusterResources template and compares them with
// already existing ones (if there are any).
//
// If there is any existing redundant resource (exist in the cluster, but not in the template), then it means that there is an ongoing update,
// so it deletes the resource and returns a result where anyDeleted=true and currentTierToBeChanged=<tier-of-the-deleted-resource>
//
// If there is any resource that is missing (does not exist in the cluster but is in the template), then it returns a result
// where toCreateOrUpdate=<missing-object-from-the-template>
//
// If there is any resource that is outdated (exists in both cluster and template but its revision or tier is not matching),
// then it means that there is an ongoing update, so it returns a result where
// toCreateOrUpdate=<object-from-template-to-be-updated> currentTierToBeChanged=<tier-of-the-outdated-resource>
func (r *clusterResourcesManager) syncMainClusterResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (mainClusterResourcesSyncResult, error) {
	logger.Info("ensuring cluster resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	username := nsTmplSet.GetName()

	// go though all main cluster resources
	for _, mainClusterResource := range mainClusterResources {
		newObjs := make([]runtime.RawExtension, 0)
		var err error
		// get all objects of the main cluster resource type from the template (if the template is specified)
		if nsTmplSet.Spec.ClusterResources != nil {
			newObjs, err = process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources),
				r.scheme, username, retainObjectsOfSameGVK(mainClusterResource.gvk))
			if err != nil {
				return noResult, errs.Wrapf(err, "failed to retrieve template for the cluster resources of GVK '%v'", mainClusterResource.gvk)
			}
		}

		// list all existing objects of the main cluster resource type
		currentObjects, err := mainClusterResource.listExistingResources(r.client, username)
		if err != nil {
			return noResult, errs.Wrapf(err, "failed to list existing cluster resources of GVK '%v'", mainClusterResource.gvk)
		}

		// if there are more than one existing, then check if there is any that should be updated or deleted
		if len(currentObjects) > 0 {
			syncResult, err := r.getMainClusterObjectToUpdateOrDeleteRedundant(logger, currentObjects, newObjs, nsTmplSet)
			if err != nil || syncResult.toCreateOrUpdate != nil || syncResult.anyDeleted {
				return syncResult, err
			}
		}
		// if none was found to be either updated or deleted or if there is no existing object available,
		// then find the first one to be created (if there is any)
		if len(newObjs) > 0 {
			return r.getMainClusterObjectToCreate(logger, currentObjects, newObjs, nsTmplSet)
		}
	}
	return noResult, nil
}

// getMainClusterObjectToUpdateOrDeleteRedundant takes the given currentObjs and newObjs and compares them.
// This method should be used only for cluster resources that are of the "main-cluster-resource" types.
//
// If there is any existing redundant resource (exist in the currentObjs, but not in the newObjs), then it means that there is an ongoing update,
// so it deletes the resource and returns a result where anyDeleted=true and currentTierToBeChanged=<tier-of-the-deleted-resource>
//
// If there is any resource that is outdated (exists in both currentObjs and newObjs but its revision or tier is not matching),
// then it means that there is an ongoing update, so it returns a result where
// toCreateOrUpdate=<object-from-template-to-be-updated> currentTierToBeChanged=<tier-of-the-outdated-resource>
func (r *clusterResourcesManager) getMainClusterObjectToUpdateOrDeleteRedundant(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (mainClusterResourcesSyncResult, error) {
	// go though all current objects so we can compare then with the set of the requested and thus update the obsolete ones or delete redundant ones
	for _, currentObject := range currentObjs {
		currObjAccessor, err := meta.Accessor(currentObject)
		if err != nil {
			return noResult, errs.Wrapf(err, "failed to get accessor of an object '%v'", currentObject)
		}

		// if the template is not specified, then delete all main cluster resources one by one
		if nsTmplSet.Spec.ClusterResources == nil {
			return r.deleteMainClusterObject(nsTmplSet, currentObject, currObjAccessor)
		}

		// if the existing object is not up-to-date, then check if it should be only updated or completely removed (in case it's missing the in set of the requested objects)
		if !isUpToDateAndProvisioned(currObjAccessor, nsTmplSet.Spec.ClusterResources.Revision, nsTmplSet.Spec.TierName) {
			for _, newObject := range newObjs {
				newObjAccessor, err := meta.Accessor(newObject.Object)
				if err != nil {
					return noResult, errs.Wrapf(err, "failed to get accessor of an object '%v'", newObject.Object)
				}
				if newObjAccessor.GetName() == currObjAccessor.GetName() {
					// is found then let's just update it
					if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
						return noResult, err
					}
					return mainClusterResourcesSyncResult{
						toCreateOrUpdate:       &newObject,
						currentTierToBeChanged: currObjAccessor.GetLabels()[toolchainv1alpha1.TierLabelKey],
					}, nil
				}
			}
			// is not found then let's delete it
			return r.deleteMainClusterObject(nsTmplSet, currentObject, currObjAccessor)
		}
	}
	return noResult, nil
}

// deleteMainClusterObject sets status to updating, deletes the given resource and returns result where anyDeleted=true and currentTierToBeChanged=<tier-of-the-deleted-resource>
func (r *clusterResourcesManager) deleteMainClusterObject(nsTmplSet *toolchainv1alpha1.NSTemplateSet, currentObject runtime.Object, currObjAccessor v1.Object) (mainClusterResourcesSyncResult, error) {
	if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
		return noResult, err
	}
	if err := r.client.Delete(context.TODO(), currentObject); err != nil {
		return noResult, errs.Wrapf(err, "failed to delete an existing redundant cluster resource of name '%s' and gvk '%v'",
			currObjAccessor.GetName(), currentObject.GetObjectKind().GroupVersionKind())
	}
	return mainClusterResourcesSyncResult{
		anyDeleted:             true,
		currentTierToBeChanged: currObjAccessor.GetLabels()[toolchainv1alpha1.TierLabelKey],
	}, nil
}

// getMainClusterObjectToCreate takes the given currentObjs and newObjs and compares them if there is any that should be created.
// This method should be used only for cluster resources that are of the "main-cluster-resource" types.
//
// If there is any resource that is missing (does not exist in currentObjs but is in newObjs), then it returns a result
// where toCreateOrUpdate=<missing-object-from-the-template>
func (r *clusterResourcesManager) getMainClusterObjectToCreate(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (mainClusterResourcesSyncResult, error) {
	// go though all new (expected) objects to check if all of them already exist or not
NewObjects:
	for _, newObject := range newObjs {
		newObjAccessor, err := meta.Accessor(newObject.Object)
		if err != nil {
			return noResult, errs.Wrapf(err, "failed to get accessor of an object '%v'", newObject.Object)
		}
		// go through current objects to check if is one of the new (expected)
		for _, currentObject := range currentObjs {
			currObjAccessor, err := meta.Accessor(currentObject)
			if err != nil {
				return noResult, errs.Wrapf(err, "failed to get accessor of an object '%v'", currentObject)
			}
			// if the name is the same, then it means that it already exist so just continue with the next new object
			if newObjAccessor.GetName() == currObjAccessor.GetName() {
				continue NewObjects
			}
		}
		// if there was no existing object found that would match with the new one, then set the status appropriately
		namespaces, err := fetchNamespaces(r.client, nsTmplSet.Name)
		if err != nil {
			return noResult, errs.Wrapf(err, "unable to fetch user's namespaces")
		}
		// if there is any existing namespace, then set the status to updating
		if len(namespaces) == 0 {
			if err := r.setStatusProvisioningIfNotUpdating(nsTmplSet); err != nil {
				return noResult, err
			}
		} else {
			// otherwise, to provisioning
			if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
				return noResult, err
			}
		}
		// and return the result containing an object to be created
		return mainClusterResourcesSyncResult{
			toCreateOrUpdate: &newObject,
		}, nil
	}
	return noResult, nil
}

// ensureNotMainClusterResources ensures that all cluster resources that are not of the "main-cluster-resource" types exist in the cluster
// If the currentTier is set, then it means that there is an ongoing update so it also checks if there are no redundant existing resources
// (exist in the cluster, but not in the template).
func (r *clusterResourcesManager) ensureNotMainClusterResources(logger logr.Logger, currentTier string, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	var newObjs []runtime.RawExtension
	var err error
	if nsTmplSet.Spec.ClusterResources != nil {
		newObjs, err = process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources), r.scheme, nsTmplSet.Name, retainAllObjectsButMainClusterResources)
		if err != nil {
			return errs.Wrapf(err, "failed to get new cluster resources from template of a tier %s", nsTmplSet.Spec.TierName)
		}
	}

	if currentTier != "" {
		currentObjs, err := process(r.templateContent(currentTier, ClusterResources), r.scheme, nsTmplSet.Name, retainAllObjectsButMainClusterResources)
		if err != nil {
			return errs.Wrapf(err, "failed to get current cluster resources from template of a tier %s", currentTier)
		}

		_, err = deleteRedundantObjects(logger, r.client, false, currentObjs, newObjs)
		if err != nil {
			return errs.Wrapf(err, "failed to delete redundant cluster objects")
		}
	}
	if len(newObjs) == 0 {
		return nil
	}

	labels := clusterResourceLabels(nsTmplSet.GetName(), nsTmplSet.Spec.ClusterResources.Revision, nsTmplSet.Spec.TierName)
	// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
	// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	logger.Info("applying cluster resources template", "obj_count", len(newObjs))
	_, err = applycl.NewApplyClient(r.client, r.scheme).Apply(newObjs, labels)
	return err
}

// delete deletes cluster scoped resources taken the ClusterResources template and returns information if any resource was deleted or not
func (r *clusterResourcesManager) delete(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	username := nsTmplSet.Name
	mainClusterObjs, err := process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources), r.scheme, username, retainAllMainClusterResourceObjects)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list main cluster resources for user '%s'", username)
	}

	logger.Info("listed cluster resources to delete", "count", len(mainClusterObjs))
	return r.deleteClusterResources(logger, nsTmplSet, true, mainClusterObjs...)
}

// deleteClusterResources deletes all toDelete resources and returns information if we should wait for another reconcile or not.
// If the given areMainClusterResources parameter is equal to true, then it deletes only one resources, triggers deletion of the not-main ones and returns true,nil.
// If the given areMainClusterResources parameter is equal to false, then it deletes all given resources and returns false,nil.
func (r *clusterResourcesManager) deleteClusterResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, areMainClusterResources bool, toDelete ...runtime.RawExtension) (bool, error) {
	username := nsTmplSet.Name
	for _, obj := range toDelete {
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
		if errors.IsNotFound(err) || util.IsBeingDeleted(objMeta) {
			continue
		}

		if areMainClusterResources {
			normalClusterObjs, err := process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources), r.scheme, username, retainAllObjectsButMainClusterResources)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list normal cluster resources for user '%s'", username)
			}
			if _, err = r.deleteClusterResources(logger, nsTmplSet, false, normalClusterObjs...); err != nil {
				return false, err
			}
		}

		logger.Info("deleting cluster resource", "name", objMeta.GetName())
		if err := r.client.Delete(context.TODO(), objectToDelete); err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", objMeta.GetName())
		}
		if areMainClusterResources {
			// stop there for now. Will reconcile again for the next main cluster resource (if any exists)
			return true, nil
		}
	}
	return false, nil
}

func retainObjectsOfSameGVK(gvk schema.GroupVersionKind) template.FilterFunc {
	return func(obj runtime.RawExtension) bool {
		return obj.Object.GetObjectKind().GroupVersionKind() == gvk
	}
}

var retainAllObjectsButMainClusterResources template.FilterFunc = func(obj runtime.RawExtension) bool {
	for _, resource := range mainClusterResources {
		if obj.Object.GetObjectKind().GroupVersionKind() == resource.gvk {
			return false
		}
	}
	return true
}

var retainAllMainClusterResourceObjects template.FilterFunc = func(obj runtime.RawExtension) bool {
	for _, resource := range mainClusterResources {
		if obj.Object.GetObjectKind().GroupVersionKind() == resource.gvk {
			return true
		}
	}
	return false
}

func clusterResourceLabels(username, revision, tier string) map[string]string {
	return map[string]string{
		toolchainv1alpha1.OwnerLabelKey:    username,
		toolchainv1alpha1.TypeLabelKey:     ClusterResources,
		toolchainv1alpha1.RevisionLabelKey: revision,
		toolchainv1alpha1.TierLabelKey:     tier,
		toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
	}
}
