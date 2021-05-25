package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type clusterResourcesManager struct {
	*statusManager
}

// listExistingResources returns a list of comparable  ToolchainObjects representing existing resources in the cluster
type listExistingResources func(cl client.Client, username string) ([]applycl.ComparableToolchainObject, error)

// toolchainObjectKind represents a resource kind that should be present in templates containing cluster resources.
// Such a kind should be watched by NSTempalateSet controller which means that every change of the
// resources of that kind triggers a new reconcile.
// It is expected that the template contains only the kinds that are being watched and there is an instance of
// toolchainObjectKind type created in clusterResourceKinds list
type toolchainObjectKind struct {
	gvk                   schema.GroupVersionKind
	objectType            runtime.Object
	listExistingResources listExistingResources
}

func newToolchainObjectKind(gvk schema.GroupVersionKind, emptyObject runtime.Object, listExistingResources listExistingResources) toolchainObjectKind {
	return toolchainObjectKind{
		gvk:                   gvk,
		objectType:            emptyObject,
		listExistingResources: listExistingResources,
	}
}

var compareNotSupported applycl.CompareToolchainObjects = func(firstObject, secondObject applycl.ToolchainObject) (bool, error) {
	return false, fmt.Errorf("objects comparison is not supported")
}

// clusterResourceKinds is a list that contains definitions for all cluster-scoped toolchainObjectKinds
var clusterResourceKinds = []toolchainObjectKind{
	newToolchainObjectKind(
		quotav1.GroupVersion.WithKind("ClusterResourceQuota"),
		&quotav1.ClusterResourceQuota{},
		func(cl client.Client, username string) ([]applycl.ComparableToolchainObject, error) {
			itemList := &quotav1.ClusterResourceQuotaList{}
			if err := cl.List(context.TODO(), itemList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]applycl.ComparableToolchainObject, len(itemList.Items))
			for index := range itemList.Items {
				toolchainObject, err := applycl.NewComparableToolchainObject(&itemList.Items[index], compareNotSupported)
				if err != nil {
					return nil, err
				}
				list[index] = toolchainObject
			}
			return list, nil
		}),

	newToolchainObjectKind(
		rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"),
		&rbacv1.ClusterRoleBinding{},
		func(cl client.Client, username string) ([]applycl.ComparableToolchainObject, error) {
			itemList := &rbacv1.ClusterRoleBindingList{}
			if err := cl.List(context.TODO(), itemList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]applycl.ComparableToolchainObject, len(itemList.Items))
			for index := range itemList.Items {
				toolchainObject, err := applycl.NewComparableToolchainObject(&itemList.Items[index], compareNotSupported)
				if err != nil {
					return nil, err
				}
				list[index] = toolchainObject
			}
			return list, nil
		}),

	newToolchainObjectKind(
		toolchainv1alpha1.GroupVersion.WithKind("Idler"),
		&toolchainv1alpha1.Idler{},
		func(cl client.Client, username string) ([]applycl.ComparableToolchainObject, error) {
			itemList := &toolchainv1alpha1.IdlerList{}
			if err := cl.List(context.TODO(), itemList, listByOwnerLabel(username)); err != nil {
				return nil, err
			}
			list := make([]applycl.ComparableToolchainObject, len(itemList.Items))
			for index := range itemList.Items {
				toolchainObject, err := applycl.NewComparableToolchainObject(&itemList.Items[index], compareNotSupported)
				if err != nil {
					return nil, err
				}
				list[index] = toolchainObject
			}
			return list, nil
		}),
}

// ensure ensures that the cluster resources exist.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
func (r *clusterResourcesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	userTierLogger := logger.WithValues("username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	userTierLogger.Info("ensuring cluster resources")
	username := nsTmplSet.GetName()
	var tierTemplate *tierTemplate
	var err error
	if nsTmplSet.Spec.ClusterResources != nil {
		tierTemplate, err = getTierTemplate(r.GetHostCluster, nsTmplSet.Spec.ClusterResources.TemplateRef)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(userTierLogger, nsTmplSet, err,
				"failed to retrieve TierTemplate for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
		}
	}
	// go though all cluster resource kinds
	for _, clusterResourceKind := range clusterResourceKinds {
		gvkLogger := userTierLogger.WithValues("gvk", clusterResourceKind.gvk)
		gvkLogger.Info("ensuring cluster resource kind")
		newObjs := make([]applycl.ToolchainObject, 0)

		// get all objects of the resource kind from the template (if the template is specified)
		if tierTemplate != nil {
			newObjs, err = tierTemplate.process(r.Scheme, username, retainObjectsOfSameGVK(clusterResourceKind.gvk))
			if err != nil {
				return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(gvkLogger, nsTmplSet, err,
					"failed to process template for the cluster resources with the name '%s'", nsTmplSet.Spec.ClusterResources.TemplateRef)
			}
		}

		// list all existing objects of the cluster resource kind
		currentObjects, err := clusterResourceKind.listExistingResources(r.Client, username)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(gvkLogger, nsTmplSet, err,
				"failed to list existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
		}

		// if there are more than one existing, then check if there is any that should be updated or deleted
		if len(currentObjects) > 0 {
			updatedOrDeleted, err := r.updateOrDeleteRedundant(gvkLogger, currentObjects, newObjs, tierTemplate, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(gvkLogger, nsTmplSet, r.setStatusUpdateFailed,
					err, "failed to update/delete existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
			}
			if updatedOrDeleted {
				return true, err
			}
		}
		// if none was found to be either updated or deleted or if there is no existing object available,
		// then check if there is any object to be created
		if len(newObjs) > 0 {
			anyCreated, err := r.createMissing(gvkLogger, currentObjects, newObjs, tierTemplate, nsTmplSet)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(gvkLogger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed,
					err, "failed to create missing cluster resource of GVK '%v'", clusterResourceKind.gvk)
			}
			if anyCreated {
				return true, nil
			}
		} else {
			gvkLogger.Info("no new cluster resources to create")
		}
	}

	userTierLogger.Info("cluster resources already provisioned")
	return false, nil
}

// apply creates or updates the given object with the set of toolchain labels. If the apply operation was successful, then it returns 'true, nil',
// but if there was an error then it returns 'false, error'.
func (r *clusterResourcesManager) apply(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate, toApply applycl.ToolchainObject) (bool, error) {
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

	logger.Info("applying cluster resource", "gvk", toApply.GetGvk())
	if _, err := applycl.NewApplyClient(r.Client, r.Scheme).ApplyToolchainObjects([]applycl.ToolchainObject{toApply}, labels); err != nil {
		return false, fmt.Errorf("failed to apply cluster resource of type '%v'", toApply.GetGvk())
	}
	return true, nil
}

// updateOrDeleteRedundant takes the given currentObjs and newObjs and compares them.
//
// If there is any existing redundant resource (exist in the currentObjs, but not in the newObjs), then it deletes the resource and returns 'true, nil'.
//
// If there is any resource that is outdated (exists in both currentObjs and newObjs but its templateref is not matching),
// then it updates the resource and returns 'true, nil'
//
// If no resource to be updated or deleted was found then it returns 'false, nil'. In case of any errors 'false, error'
func (r *clusterResourcesManager) updateOrDeleteRedundant(logger logr.Logger, currentObjs []applycl.ComparableToolchainObject, newObjs []applycl.ToolchainObject, tierTemplate *tierTemplate, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// go though all current objects so we can compare then with the set of the requested and thus update the obsolete ones or delete redundant ones
CurrentObjects:
	for _, currentObject := range currentObjs {

		// if the template is not specified, then delete all cluster resources one by one
		if nsTmplSet.Spec.ClusterResources == nil || tierTemplate == nil {
			return r.deleteClusterResource(nsTmplSet, currentObject)
		}

		// check if the object should still exist and should be updated
		for _, newObject := range newObjs {
			if newObject.HasSameName(currentObject) {
				// is found then let's check if we need to update it
				if !isUpToDate(currentObject, newObject, tierTemplate) {
					// let's update it
					if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
						return false, err
					}
					return r.apply(logger, nsTmplSet, tierTemplate, newObject)
				}
				continue CurrentObjects
			}
		}
		// is not found then let's delete it
		return r.deleteClusterResource(nsTmplSet, currentObject)
	}
	return false, nil
}

// deleteClusterResource sets status to updating, deletes the given resource and returns 'true, nil'. In case of any errors 'false, error'.
func (r *clusterResourcesManager) deleteClusterResource(nsTmplSet *toolchainv1alpha1.NSTemplateSet, toDelete applycl.ToolchainObject) (bool, error) {
	if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
		return false, err
	}
	if err := r.Client.Delete(context.TODO(), toDelete.GetRuntimeObject()); err != nil {
		return false, errs.Wrapf(err, "failed to delete an existing redundant cluster resource of name '%s' and gvk '%v'",
			toDelete.GetName(), toDelete.GetGvk())
	}
	return true, nil
}

// createMissing takes the given currentObjs and newObjs and compares them if there is any that should be created.
// If such a object is found, then it creates it and returns 'true, nil'. If no missing resource was found then returns 'false, nil'.
// In case of any error 'false, error'
func (r *clusterResourcesManager) createMissing(logger logr.Logger, currentObjs []applycl.ComparableToolchainObject, newObjs []applycl.ToolchainObject, tierTemplate *tierTemplate, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// go though all new (expected) objects to check if all of them already exist or not
NewObjects:
	for _, newObject := range newObjs {

		// go through current objects to check if is one of the new (expected)
		for _, currentObject := range currentObjs {
			// if the name is the same, then it means that it already exist so just continue with the next new object
			if newObject.HasSameName(currentObject) {
				continue NewObjects
			}
		}
		// if there was no existing object found that would match with the new one, then set the status appropriately
		namespaces, err := fetchNamespaces(r.Client, nsTmplSet.Name)
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
		currentObjects, err := clusterResourceKind.listExistingResources(r.Client, nsTmplSet.Name)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForClusterResourceFailure(logger, nsTmplSet, err,
				"failed to list existing cluster resources of GVK '%v'", clusterResourceKind.gvk)
		}

		for _, toDelete := range currentObjects {
			if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: toDelete.GetName()}, toDelete.GetRuntimeObject()); err != nil && !errors.IsNotFound(err) {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err,
					"failed to get current object '%s' while deleting cluster resource of GVK '%s'", toDelete.GetName(), toDelete.GetGvk())
			}
			// ignore cluster resource that are already flagged for deletion
			if errors.IsNotFound(err) || util.IsBeingDeleted(toDelete) {
				continue
			}

			logger.Info("deleting cluster resource", "name", toDelete.GetName(), "kind", toDelete.GetGvk().Kind)
			if err := r.Client.Delete(context.TODO(), toDelete.GetRuntimeObject()); err != nil && errors.IsNotFound(err) {
				// ignore case where the resource did not exist anymore, move to the next one to delete
				continue
			} else if err != nil {
				// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", toDelete.GetName())
			}
			// stop there for now. Will reconcile again for the next cluster resource (if any exists)
			return true, nil
		}
	}
	return false, nil
}

// isUpToDate returns true if the currentObject uses the corresponding templateRef and equals to the new object
func isUpToDate(currentObject, _ applycl.ToolchainObject, tierTemplate *tierTemplate) bool {
	return currentObject.GetLabels()[toolchainv1alpha1.TemplateRefLabelKey] != "" &&
		currentObject.GetLabels()[toolchainv1alpha1.TemplateRefLabelKey] == tierTemplate.templateRef
	// && currentObject.IsSame(newObject)  <-- TODO Uncomment when IsSame is implemented for all ToolchainObjects!
}

func retainObjectsOfSameGVK(gvk schema.GroupVersionKind) template.FilterFunc {
	return func(obj runtime.RawExtension) bool {
		return obj.Object.GetObjectKind().GroupVersionKind() == gvk
	}
}
