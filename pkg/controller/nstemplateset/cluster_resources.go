package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/api/rbac/v1alpha1"
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

// watchedClusterResource represents a resource type that should be present in all templates containing cluster resources
// "watched-cluster-resource-type" should be watched by NSTempalateSet controller which means that every change of the
// resources of that type triggers a new reconcile
type watchedClusterResource struct {
	gvk                   schema.GroupVersionKind
	object                runtime.Object
	listExistingResources getResourceList
}

// getResourceList returns a list of objects representing existing resources for the given user
type getResourceList func(cl client.Client, username string) ([]runtime.Object, error)

// watchedClusterResources is a list that contains definitions for all "watched-cluster-resource" types
var watchedClusterResources = []watchedClusterResource{
	{
		gvk:    quotav1.GroupVersion.WithKind("ClusterResourceQuota"),
		object: &quotav1.ClusterResourceQuota{},
		listExistingResources: func(cl client.Client, username string) ([]runtime.Object, error) {
			itemList := &quotav1.ClusterResourceQuotaList{}
			if err := cl.List(context.TODO(), itemList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]runtime.Object, len(itemList.Items))
			for index := range itemList.Items {
				list[index] = &itemList.Items[index]
			}
			return list, nil
		},
	},
	{
		gvk:    v1alpha1.SchemeGroupVersion.WithKind("ClusterRoleBinding"),
		object: &v1alpha1.ClusterRoleBinding{},
		listExistingResources: func(cl client.Client, username string) ([]runtime.Object, error) {
			itemList := &v1alpha1.ClusterRoleBindingList{}
			if err := cl.List(context.TODO(), itemList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]runtime.Object, len(itemList.Items))
			for index := range itemList.Items {
				list[index] = &itemList.Items[index]
			}
			return list, nil
		},
	},
}

// ensure ensures that the cluster resources exists.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
func (r *clusterResourcesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {

	logger.Info("ensuring cluster resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	username := nsTmplSet.GetName()

	// go though all watched cluster resources
	for _, watchedClusterResource := range watchedClusterResources {
		newObjs := make([]runtime.RawExtension, 0)
		var err error
		// get all objects of the resource type from the template (if the template is specified)
		if nsTmplSet.Spec.ClusterResources != nil {
			newObjs, err = process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources),
				r.scheme, username, retainObjectsOfSameGVK(watchedClusterResource.gvk))
			if err != nil {
				return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
					"failed to retrieve template for the cluster resources of GVK '%v'", watchedClusterResource.gvk)
			}
		}

		// list all existing objects of the watched cluster resource type
		currentObjects, err := watchedClusterResource.listExistingResources(r.client, username)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
				"failed to list existing cluster resources of GVK '%v'", watchedClusterResource.gvk)
		}

		// if there are more than one existing, then check if there is any that should be updated or deleted
		if len(currentObjects) > 0 {
			updatedOrDeleted, err := r.updateOrDeleteRedundant(logger, currentObjects, newObjs, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed,
					err, "failed to update/delete existing cluster resources of GVK '%v'", watchedClusterResource.gvk)
			}
			if updatedOrDeleted {
				return true, err
			}
		}
		// if none was found to be either updated or deleted or if there is no existing object available,
		// then find the first one to be created (if there is any)
		if len(newObjs) > 0 {
			anyCreated, err := r.createMissing(logger, currentObjects, newObjs, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed,
					err, "failed to create missing cluster resource of GVK '%v'", watchedClusterResource.gvk)
			}
			if anyCreated {
				return true, nil
			}
		}
	}

	logger.Info("cluster resources already provisioned")
	return false, nil
}

// syncWatchedClusterResources takes the objects that are of the "watched-cluster-resource" types from the ClusterResources template and compares them with
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
//func (r *clusterResourcesManager) syncWatchedClusterResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
//
//}

func (r *clusterResourcesManager) apply(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, toApply runtime.RawExtension) (bool, error) {
	labels := clusterResourceLabels(nsTmplSet.GetName(), nsTmplSet.Spec.ClusterResources.Revision, nsTmplSet.Spec.TierName)
	// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
	// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	logger.Info("applying watched cluster resource", "gvk", toApply.Object.GetObjectKind().GroupVersionKind())
	if _, err := applycl.NewApplyClient(r.client, r.scheme).Apply([]runtime.RawExtension{toApply}, labels); err != nil {
		return false, fmt.Errorf("failed to apply watched cluster resource of type '%v'", toApply.Object.GetObjectKind().GroupVersionKind())
	}
	return true, nil
}

// updateOrDeleteRedundant takes the given currentObjs and newObjs and compares them.
// This method should be used only for cluster resources that are of the "watched-cluster-resource" types.
//
// If there is any existing redundant resource (exist in the currentObjs, but not in the newObjs), then it means that there is an ongoing update,
// so it deletes the resource and returns a result where anyDeleted=true and currentTierToBeChanged=<tier-of-the-deleted-resource>
//
// If there is any resource that is outdated (exists in both currentObjs and newObjs but its revision or tier is not matching),
// then it means that there is an ongoing update, so it returns a result where
// toCreateOrUpdate=<object-from-template-to-be-updated> currentTierToBeChanged=<tier-of-the-outdated-resource>
func (r *clusterResourcesManager) updateOrDeleteRedundant(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// go though all current objects so we can compare then with the set of the requested and thus update the obsolete ones or delete redundant ones
	for _, currentObject := range currentObjs {
		currObjAccessor, err := meta.Accessor(currentObject)
		if err != nil {
			return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", currentObject)
		}

		// if the template is not specified, then delete all watched cluster resources one by one
		if nsTmplSet.Spec.ClusterResources == nil {
			return r.deleteWatchedClusterObject(nsTmplSet, currentObject, currObjAccessor)
		}

		// if the existing object is not up-to-date, then check if it should be only updated or completely removed (in case it's missing the in set of the requested objects)
		if !isUpToDateAndProvisioned(currObjAccessor, nsTmplSet.Spec.ClusterResources.Revision, nsTmplSet.Spec.TierName) {
			for _, newObject := range newObjs {
				newObjAccessor, err := meta.Accessor(newObject.Object)
				if err != nil {
					return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", newObject.Object)
				}
				if newObjAccessor.GetName() == currObjAccessor.GetName() {
					// is found then let's just update it
					if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
						return false, err
					}
					return r.apply(logger, nsTmplSet, newObject)
				}
			}
			// is not found then let's delete it
			return r.deleteWatchedClusterObject(nsTmplSet, currentObject, currObjAccessor)
		}
	}
	return false, nil
}

// deleteWatchedClusterObject sets status to updating, deletes the given resource and returns result where anyDeleted=true and currentTierToBeChanged=<tier-of-the-deleted-resource>
func (r *clusterResourcesManager) deleteWatchedClusterObject(nsTmplSet *toolchainv1alpha1.NSTemplateSet, currentObject runtime.Object, currObjAccessor v1.Object) (bool, error) {
	if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
		return false, err
	}
	if err := r.client.Delete(context.TODO(), currentObject); err != nil {
		return false, errs.Wrapf(err, "failed to delete an existing redundant cluster resource of name '%s' and gvk '%v'",
			currObjAccessor.GetName(), currentObject.GetObjectKind().GroupVersionKind())
	}
	return true, nil
}

// createMissing takes the given currentObjs and newObjs and compares them if there is any that should be created.
// This method should be used only for cluster resources that are of the "watched-cluster-resource" types.
//
// If there is any resource that is missing (does not exist in currentObjs but is in newObjs), then it returns a result
// where toCreateOrUpdate=<missing-object-from-the-template>
func (r *clusterResourcesManager) createMissing(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// go though all new (expected) objects to check if all of them already exist or not
NewObjects:
	for _, newObject := range newObjs {
		newObjAccessor, err := meta.Accessor(newObject.Object)
		if err != nil {
			return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", newObject.Object)
		}
		// go through current objects to check if is one of the new (expected)
		for _, currentObject := range currentObjs {
			currObjAccessor, err := meta.Accessor(currentObject)
			if err != nil {
				return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", currentObject)
			}
			// if the name is the same, then it means that it already exist so just continue with the next new object
			if newObjAccessor.GetName() == currObjAccessor.GetName() {
				continue NewObjects
			}
		}
		// if there was no existing object found that would match with the new one, then set the status appropriately
		namespaces, err := fetchNamespaces(r.client, nsTmplSet.Name)
		if err != nil {
			return false, errs.Wrapf(err, "unable to fetch user's namespaces")
		}
		// if there is any existing namespace, then set the status to updating
		if len(namespaces) == 0 {
			if err := r.setStatusProvisioningIfNotUpdating(nsTmplSet); err != nil {
				return false, err
			}
		} else {
			// otherwise, to provisioning
			if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
				return false, err
			}
		}
		return r.apply(logger, nsTmplSet, newObject)
	}
	return false, nil
}

// delete deletes cluster scoped resources taken the ClusterResources template and returns information if any resource was deleted or not
func (r *clusterResourcesManager) delete(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	username := nsTmplSet.Name
	watchedClusterObjs, err := process(r.templateContent(nsTmplSet.Spec.TierName, ClusterResources), r.scheme, username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list watched cluster resources for user '%s'", username)
	}

	logger.Info("listed cluster resources to delete", "count", len(watchedClusterObjs))
	return r.deleteClusterResources(logger, nsTmplSet, true, watchedClusterObjs...)
}

// deleteClusterResources deletes all toDelete resources and returns information if we should wait for another reconcile or not.
// If the given areWatchedClusterResources parameter is equal to true, then it deletes only one resources, triggers deletion of the not-watched ones and returns true,nil.
// If the given areWatchedClusterResources parameter is equal to false, then it deletes all given resources and returns false,nil.
func (r *clusterResourcesManager) deleteClusterResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, areWatchedClusterResources bool, toDelete ...runtime.RawExtension) (bool, error) {
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

		logger.Info("deleting cluster resource", "name", objMeta.GetName())
		if err := r.client.Delete(context.TODO(), objectToDelete); err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", objMeta.GetName())
		}
		// stop there for now. Will reconcile again for the next watched cluster resource (if any exists)
		return true, nil
	}
	return false, nil
}

func retainObjectsOfSameGVK(gvk schema.GroupVersionKind) template.FilterFunc {
	return func(obj runtime.RawExtension) bool {
		return obj.Object.GetObjectKind().GroupVersionKind() == gvk
	}
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
