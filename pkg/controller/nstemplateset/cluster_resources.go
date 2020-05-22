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
	rbacv1 "k8s.io/api/rbac/v1"
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

// clusterResourceKind represents a resource kind that should be present in templates containing cluster resources.
// Such a kind should be watched by NSTempalateSet controller which means that every change of the
// resources of that kind triggers a new reconcile.
// It is expected that the template contains only the kinds that are being watched and there is an instance of
// clusterResourceKind type created in clusterResourceKinds list
type clusterResourceKind struct {
	gvk                   schema.GroupVersionKind
	object                runtime.Object
	listExistingResources getResourceList
}

// getResourceList returns a list of objects representing existing resources for the given user
type getResourceList func(cl client.Client, username string) ([]runtime.Object, error)

// clusterResourceKinds is a list that contains definitions for all cluster resource kinds
var clusterResourceKinds = []clusterResourceKind{
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
		gvk:    rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"),
		object: &rbacv1.ClusterRoleBinding{},
		listExistingResources: func(cl client.Client, username string) ([]runtime.Object, error) {
			itemList := &rbacv1.ClusterRoleBindingList{}
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

// ensure ensures that the cluster resources exist.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
func (r *clusterResourcesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	logger.Info("ensuring cluster resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	username := nsTmplSet.GetName()
	var tierTemplate *tierTemplate
	var err error
	if nsTmplSet.Spec.ClusterResources != nil {
		tierTemplate, err = r.getTemplateContent(nsTmplSet.Spec.ClusterResources.TemplateRef)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
				"failed to retrieve TierTemplate for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
		}
	}
	// go though all cluster resource kinds
	for _, clusterResourceKind := range clusterResourceKinds {
		newObjs := make([]runtime.RawExtension, 0)

		// get all objects of the resource kind from the template (if the template is specified)
		if tierTemplate != nil {
			newObjs, err = tierTemplate.process(r.scheme, username, retainObjectsOfSameGVK(clusterResourceKind.gvk))
			if err != nil {
				return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
					"failed to process template for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
			}
		}

		// list all existing objects of the cluster resource kind
		currentObjects, err := clusterResourceKind.listExistingResources(r.client, username)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
				"failed to list existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
		}

		// if there are more than one existing, then check if there is any that should be updated or deleted
		if len(currentObjects) > 0 {
			updatedOrDeleted, err := r.updateOrDeleteRedundant(logger, currentObjects, newObjs, tierTemplate, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed,
					err, "failed to update/delete existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
			}
			if updatedOrDeleted {
				return true, err
			}
		}
		// if none was found to be either updated or deleted or if there is no existing object available,
		// then check if there is any object to be created
		if len(newObjs) > 0 {
			anyCreated, err := r.createMissing(logger, currentObjects, newObjs, tierTemplate, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed,
					err, "failed to create missing cluster resource of GVK '%v'", clusterResourceKind.gvk)
			}
			if anyCreated {
				return true, nil
			}
		}
	}

	logger.Info("cluster resources already provisioned")
	return false, nil
}

// apply creates or updates the given object with the set of toolchain labels. If the apply operation was successful, then it returns 'true, nil',
// but if there was an error then it returns 'false, error'.
func (r *clusterResourcesManager) apply(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate, toApply runtime.RawExtension) (bool, error) {
	var labels = map[string]string{
		toolchainv1alpha1.OwnerLabelKey:       nsTmplSet.GetName(),
		toolchainv1alpha1.TypeLabelKey:        clusterResourcesType,
		toolchainv1alpha1.TemplateRefLabelKey: tierTemplate.templateRef,
		toolchainv1alpha1.TierLabelKey:        tierTemplate.tierName,
		toolchainv1alpha1.ProviderLabelKey:    toolchainv1alpha1.ProviderLabelValue,
	}
	// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
	// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated cluster-wide resources that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	logger.Info("applying cluster resource", "gvk", toApply.Object.GetObjectKind().GroupVersionKind())
	if _, err := applycl.NewApplyClient(r.client, r.scheme).Apply([]runtime.RawExtension{toApply}, labels); err != nil {
		return false, fmt.Errorf("failed to apply cluster resource of type '%v'", toApply.Object.GetObjectKind().GroupVersionKind())
	}
	return true, nil
}

// updateOrDeleteRedundant takes the given currentObjs and newObjs and compares them.

// If there is any existing redundant resource (exist in the currentObjs, but not in the newObjs), then it deletes the resource and returns 'true, nil'.
//
// If there is any resource that is outdated (exists in both currentObjs and newObjs but its templateref is not matching),
// then it updates the resource and returns 'true, nil'
//
// If no resource to be updated or deleted was found then it returns 'false, nil'. In case of any errors 'false, error'
func (r *clusterResourcesManager) updateOrDeleteRedundant(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, tierTemplate *tierTemplate, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// go though all current objects so we can compare then with the set of the requested and thus update the obsolete ones or delete redundant ones
	for _, currentObject := range currentObjs {
		currObjAccessor, err := meta.Accessor(currentObject)
		if err != nil {
			return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", currentObject)
		}

		// if the template is not specified, then delete all cluster resources one by one
		if nsTmplSet.Spec.ClusterResources == nil || tierTemplate == nil {
			return r.deleteClusterResource(nsTmplSet, currentObject, currObjAccessor)
		}

		// if the existing object is not up-to-date, then check if it should be only updated or completely removed (in case it's missing the in set of the requested objects)
		if !isUpToDateAndProvisioned(currObjAccessor, tierTemplate) {
			for _, newObject := range newObjs {
				newObjAccessor, err := meta.Accessor(newObject.Object)
				if err != nil {
					return false, errs.Wrapf(err, "failed to get accessor of an object '%v'", newObject.Object)
				}
				if newObjAccessor.GetName() == currObjAccessor.GetName() {
					// is found then let's update it
					if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
						return false, err
					}
					return r.apply(logger, nsTmplSet, tierTemplate, newObject)
				}
			}
			// is not found then let's delete it
			return r.deleteClusterResource(nsTmplSet, currentObject, currObjAccessor)
		}
	}
	return false, nil
}

// deleteClusterResource sets status to updating, deletes the given resource and returns 'true, nil'. In case of any errors 'false, error'.
func (r *clusterResourcesManager) deleteClusterResource(nsTmplSet *toolchainv1alpha1.NSTemplateSet, currentObject runtime.Object, currObjAccessor v1.Object) (bool, error) {
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
// If such a object is found, then it creates it and returns 'true, nil'. If no missing resource was found then returns 'false, nil'.
// In case of any error 'false, error'
func (r *clusterResourcesManager) createMissing(logger logr.Logger, currentObjs []runtime.Object, newObjs []runtime.RawExtension, tierTemplate *tierTemplate, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
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
		// and create the object
		return r.apply(logger, nsTmplSet, tierTemplate, newObject)
	}
	return false, nil
}

// delete deletes one cluster scoped resource owned by the user and returns 'true, nil'. If no cluster-scoped resource owned
// by the user is found, then it returns 'false, nil'. In case of any errors 'false, error'
func (r *clusterResourcesManager) delete(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	if nsTmplSet.Spec.ClusterResources == nil {
		return false, nil
	}
	for _, clusterResourceKind := range clusterResourceKinds {
		// list all existing objects of the cluster resource kind
		currentObjects, err := clusterResourceKind.listExistingResources(r.client, nsTmplSet.Name)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
				"failed to list existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
		}

		for _, objectToDelete := range currentObjects {
			objMeta, err := meta.Accessor(objectToDelete)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err,
					"failed to delete cluster resource of kind '%s'", objectToDelete.GetObjectKind())
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
			// stop there for now. Will reconcile again for the next cluster resource (if any exists)
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
