package nstemplateset

import (
	"context"
	"fmt"
	"sort"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type namespacesManager struct {
	*statusManager
}

// ensure ensures that all expected namespaces exists and they contain all the expected resources
// return `true, nil` when something changed, `false, nil` or `false, err` otherwise
func (r *namespacesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (createdOrUpdated bool, err error) {
	username := nsTmplSet.GetName()
	userNamespaces, err := fetchNamespaces(r.Client, username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err, "failed to list namespaces with label owner '%s'", username)
	}

	tierTemplatesByType, err := r.getTierTemplatesForAllNamespaces(nsTmplSet)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to get TierTemplates for tier '%s'", nsTmplSet.Spec.TierName)
	}
	toDeprovision, found := nextNamespaceToDeprovision(tierTemplatesByType, userNamespaces)
	if found {
		if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
			return false, err
		}
		if err := r.Client.Delete(context.TODO(), toDeprovision); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete namespace %s", toDeprovision.Name)
		}
		logger.Info("deleted namespace as part of NSTemplateSet update", "namespace", toDeprovision.Name)
		return true, nil // we deleted the namespace - wait for another reconcile
	}

	// find next namespace for provisioning namespace resource
	tierTemplate, userNamespace, found := nextNamespaceToProvisionOrUpdate(tierTemplatesByType, userNamespaces)
	if !found {
		logger.Info("no more namespaces to create", "username", nsTmplSet.GetName())
		return false, nil
	}

	if len(userNamespaces) > 0 {
		if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
			return false, err
		}
	} else {
		if err := r.setStatusProvisioningIfNotUpdating(nsTmplSet); err != nil {
			return false, err
		}
	}
	// create namespace resource
	return true, r.ensureNamespace(logger, nsTmplSet, tierTemplate, userNamespace)
}

// ensureNamespace ensures that the namespace exists and that it contains all the expected resources
func (r *namespacesManager) ensureNamespace(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate, userNamespace *corev1.Namespace) error {
	logger.Info("ensuring namespace", "namespace", tierTemplate.typeName, "tier", nsTmplSet.Spec.TierName)

	// create namespace before created inner resources because creating the namespace may take some time
	if userNamespace == nil {
		return r.ensureNamespaceResource(logger, nsTmplSet, tierTemplate)
	}
	return r.ensureInnerNamespaceResources(logger, nsTmplSet, tierTemplate, userNamespace)
}

// ensureNamespaceResource ensures that the namespace exists.
func (r *namespacesManager) ensureNamespaceResource(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate) error {
	logger.Info("creating namespace", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName, "type", tierTemplate.typeName)

	objs, err := tierTemplate.process(r.Scheme, nsTmplSet.GetName(), template.RetainNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to process template for namespace type '%s'", tierTemplate.typeName)
	}

	labels := map[string]string{
		toolchainv1alpha1.OwnerLabelKey:    nsTmplSet.GetName(),
		toolchainv1alpha1.TypeLabelKey:     tierTemplate.typeName,
		toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
	}

	// Note: we don't see an owner reference between the NSTemplateSet (namespaced resource) and the namespace (cluster-wide resource)
	// because a namespaced resource cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
	// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated namespaces that belong to the same user.
	// see https://issues.redhat.com/browse/CRT-429

	_, err = applycl.NewApplyClient(r.Client, r.Scheme).ApplyToolchainObjects(objs, labels)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to create namespace with type '%s'", tierTemplate.typeName)
	}
	logger.Info("namespace provisioned", "namespace", tierTemplate)
	return nil
}

// ensureInnerNamespaceResources ensure that the namespace has the expected resources.
func (r *namespacesManager) ensureInnerNamespaceResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tierTemplate *tierTemplate, namespace *corev1.Namespace) error {
	logger.Info("ensuring namespace resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName, "type", tierTemplate.typeName)
	nsName := namespace.GetName()
	username := nsTmplSet.GetName()
	newObjs, err := tierTemplate.process(r.Scheme, username, template.RetainAllButNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to process template for namespace '%s'", nsName)
	}

	if currentRef, exists := namespace.Labels[toolchainv1alpha1.TemplateRefLabelKey]; exists && currentRef != "" && currentRef != tierTemplate.templateRef {
		if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
			return err
		}
		currentTierTemplate, err := getTierTemplate(r.GetHostCluster, currentRef)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to retrieve current TierTemplate with name '%s'", currentRef)
		}
		currentObjs, err := currentTierTemplate.process(r.Scheme, username, template.RetainAllButNamespaces)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to process template for TierTemplate with name '%s'", currentRef)
		}
		if _, err := deleteRedundantObjects(logger, r.Client, false, currentObjs, newObjs); err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant objects in namespace '%s'", nsName)
		}
	}

	var labels = map[string]string{
		toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
	}
	if _, err = applycl.NewApplyClient(r.Client, r.Scheme).ApplyToolchainObjects(newObjs, labels); err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s' with required resources", nsName)
	}

	if namespace.Labels == nil {
		namespace.Labels = make(map[string]string)
	}

	// Adding label indicating that the namespace is up-to-date with TierTemplate
	namespace.Labels[toolchainv1alpha1.TemplateRefLabelKey] = tierTemplate.templateRef
	namespace.Labels[toolchainv1alpha1.TierLabelKey] = tierTemplate.tierName
	if err := r.Client.Update(context.TODO(), namespace); err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to update namespace '%s'", nsName)
	}

	logger.Info("namespace provisioned with all required resources", "templateRef", tierTemplate.templateRef)

	// TODO add validation for other objects
	return nil // nothing changed, no error occurred
}

// ensureDeleted ensures that the namespaces that are owned by the user (based on the label) are deleted.
// The method deletes only one namespace in one call.
// It returns true if all the namespaces are gone and returns false if we should re-try:
//     If there is no namespaces found then it returns true, nil.
//     If there is still some namespace which is not already in terminating state then it triggers
//        the deletion of the namespace (one namespace in one call) and returns false, nil
//     If a namespace deletion was triggered previously but is not complete yet (namespace is in terminating state)
//        then it updates the status of the NSTemplateSet stating that some of the namespace is still in terminating state and returns false, nil.
// If some error happened then it returns false, error
func (r *namespacesManager) ensureDeleted(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	// now, we can delete all "child" namespaces explicitly
	username := nsTmplSet.Name
	userNamespaces, err := fetchNamespaces(r.Client, username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list namespace with label owner '%s'", username)
	}

	if len(userNamespaces) == 0 {
		return true, nil // All namespaces are gone
	}
	ns := userNamespaces[0]
	if !util.IsBeingDeleted(&ns) {
		logger.Info("deleting a user namespace associated with the deleted NSTemplateSet", "namespace", ns.Name)
		if err := r.Client.Delete(context.TODO(), &ns); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete user namespace '%s'", ns.Name)
		}
		return false, nil // The namespace deletion is triggered so we should stop here. When the namespace is actually deleted the reconcile will be triggered again
	}
	// implies namespace has a deletion timestamp but has not been deleted yet, update status and returns false so we will re-try when the namespace is actually deleted
	return false, r.setStatusTerminatingFailed(nsTmplSet, fmt.Sprintf("user namespace %s deletion was triggered but is not complete yet, something could be blocking ns deletion", ns.Name))
}

func (r *namespacesManager) getTierTemplatesForAllNamespaces(nsTmplSet *toolchainv1alpha1.NSTemplateSet) ([]*tierTemplate, error) {
	var tmpls []*tierTemplate
	for _, ns := range nsTmplSet.Spec.Namespaces {
		nsTmpl, err := getTierTemplate(r.GetHostCluster, ns.TemplateRef)
		if err != nil {
			return nil, err
		}
		tmpls = append(tmpls, nsTmpl)
	}
	return tmpls, nil
}

// fetchNamespaces returns all current namespaces belonging to the given user
// i.e., labeled with `"toolchain.dev.openshift.com/owner":<username>`
func fetchNamespaces(client client.Client, username string) ([]corev1.Namespace, error) {
	// fetch all namespace with owner=username label
	userNamespaceList := &corev1.NamespaceList{}
	if err := client.List(context.TODO(), userNamespaceList, listByOwnerLabel(username)); err != nil {
		return nil, err
	}
	names := make([]string, len(userNamespaceList.Items))
	for i, ns := range userNamespaceList.Items {
		names[i] = ns.Name
	}
	sort.Strings(names)
	sortedNamespaces := make([]corev1.Namespace, len(userNamespaceList.Items))
	for i, name := range names {
		for _, ns := range userNamespaceList.Items {
			if ns.Name == name {
				sortedNamespaces[i] = ns
				break
			}
		}
	}
	return userNamespaceList.Items, nil
}

// nextNamespaceToProvisionOrUpdate returns first namespace (from given namespaces) whose status is active and
// either revision is not set or revision or tier doesn't equal to the current one.
// It also returns namespace present in tcNamespaces but not found in given namespaces
func nextNamespaceToProvisionOrUpdate(tierTemplatesByType []*tierTemplate, namespaces []corev1.Namespace) (*tierTemplate, *corev1.Namespace, bool) {
	for _, nsTemplate := range tierTemplatesByType {
		namespace, found := findNamespace(namespaces, nsTemplate.typeName)
		if found {
			if namespace.Status.Phase == corev1.NamespaceActive {
				if !isUpToDateAndProvisioned(&namespace, nsTemplate) {
					return nsTemplate, &namespace, true
				}
			}
		} else {
			return nsTemplate, nil, true
		}
	}
	return nil, nil, false
}

// nextNamespaceToDeprovision returns namespace (and information of it was found) that should be deprovisioned
// because its type wasn't found in the set of namespace types in NSTemplateSet
func nextNamespaceToDeprovision(tierTemplatesByType []*tierTemplate, namespaces []corev1.Namespace) (*corev1.Namespace, bool) {
Namespaces:
	for _, ns := range namespaces {
		for _, nsTemplate := range tierTemplatesByType {
			if nsTemplate.typeName == ns.Labels[toolchainv1alpha1.TypeLabelKey] {
				continue Namespaces
			}
		}
		return &ns, true
	}
	return nil, false
}

func findNamespace(namespaces []corev1.Namespace, typeName string) (corev1.Namespace, bool) {
	for _, ns := range namespaces {
		if ns.Labels[toolchainv1alpha1.TypeLabelKey] == typeName {
			return ns, true
		}
	}
	return corev1.Namespace{}, false
}

func getNamespaceName(request reconcile.Request) (string, error) {
	namespace := request.Namespace
	if namespace == "" {
		return configuration.GetWatchNamespace()
	}
	return namespace, nil
}
