package nstemplateset

import (
	"context"
	"encoding/json"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

type spaceRolesManager struct {
	*statusManager
}

// ensure ensures that the space roles for the users exist.
// Returns `true, nil` if something was changed, `false, nil` if nothing changed, `false, err` if an error occurred
// TODO: support update/removal/outdated
func (r *spaceRolesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	logger = logger.WithValues("nstemplateset_name", nsTmplSet.Name)

	nss, err := fetchNamespacesByWorkspace(r.Client, nsTmplSet.Name)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
			"failed to list namespaces for workspace '%s'", nsTmplSet.Name)
	}
	logger.Info("ensuring space roles", "namespace_count", len(nss), "role_count", len(nsTmplSet.Spec.SpaceRoles))
	changed := false
	for _, ns := range nss {
		// space roles to apply now
		lastAppliedSpaceRoleObjs, err := r.getLastAppliedSpaceRoles(&ns)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForSpaceRolesFailure(logger, nsTmplSet, err, "failed to retrieve last applied space roles")
		}
		spaceRoleObjs, err := r.getSpaceRoles(&ns, nsTmplSet.Spec.SpaceRoles)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForSpaceRolesFailure(logger, nsTmplSet, err, "failed to retrieve space roles to apply")
		}

		deleted, err := deleteObsoleteObjects(logger, r.Client, lastAppliedSpaceRoleObjs, spaceRoleObjs)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant objects in namespace '%s'", ns.Name)
		}
		changed = changed || deleted
		// labels to apply on all new objects
		var labels = map[string]string{
			toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
			toolchainv1alpha1.OwnerLabelKey:    nsTmplSet.GetName(), // TODO: is it needed?
		}
		logger.Info("creating space role objects")
		for _, obj := range spaceRoleObjs {
			logger.Info(fmt.Sprintf("creating/updating %s/%s", obj.GetNamespace(), obj.GetName()))
		}
		// create (or update existing) objects based the tier template
		deleted, err = applycl.NewApplyClient(r.Client, r.Scheme).Apply(spaceRoleObjs, labels)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s' with space roles", ns.Name)
		}
		changed = changed || deleted
		if changed {
			// store the space roles in an annotation at the namespace level, so we know what was applied and how to deal with
			// diffs when the space roles are changed (users added or removed, etc.)
			sr, _ := json.Marshal(nsTmplSet.Spec.SpaceRoles)
			if ns.Annotations == nil {
				ns.Annotations = map[string]string{}
			}
			ns.Annotations[toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey] = string(sr)
			if err := r.Client.Update(context.TODO(), &ns); err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
					"failed update namespace annotation")
			}
		}
	}
	return changed, nil
}

// Get the space role objects from the templates specified in the given `spaceRoles`
// Returns the objects indexed by kind and name, or an error if something wrong happened when processing the templates
func (r *spaceRolesManager) getSpaceRoles(ns *corev1.Namespace, spaceRoles []toolchainv1alpha1.NSTemplateSetSpaceRole) ([]runtimeclient.Object, error) {
	// store by kind and name
	spaceRoleObjects := []runtimeclient.Object{}
	for _, spaceRole := range spaceRoles {
		tierTemplate, err := getTierTemplate(r.GetHostCluster, spaceRole.TemplateRef)
		if err != nil {
			return nil, err
		}
		for _, username := range spaceRole.Usernames {
			objs, err := tierTemplate.process(r.Scheme, map[string]string{
				Namespace: ns.Name,
				Username:  username,
			})
			if err != nil {
				return nil, errors.Wrapf(err, "failed to process space roles template '%s' for the user '%s' in namespace '%s'", spaceRole.TemplateRef, username, ns.Name)
			}
			spaceRoleObjects = append(spaceRoleObjects, objs...)
		}
	}
	return spaceRoleObjects, nil
}

// looks-up the `toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey` annotation on the namespace to find the last-applied space roles
// Returns the objects indexed by kind and name, or an error
func (r *spaceRolesManager) getLastAppliedSpaceRoles(ns *corev1.Namespace) ([]runtimeclient.Object, error) {
	// read annotation to see what was applied last time, so we can compare with the new SpaceRoles and remove all outdated resources (based on their kind/names)
	spaceRoles := []toolchainv1alpha1.NSTemplateSetSpaceRole{}
	if currentSpaceRolesAnnotation, exists := ns.Annotations[toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey]; exists && currentSpaceRolesAnnotation != "" {
		if err := json.Unmarshal([]byte(currentSpaceRolesAnnotation), &spaceRoles); err != nil {
			return nil, errors.Wrap(err, "unable to decode current space roles in annotation")
		}
	}
	return r.getSpaceRoles(ns, spaceRoles)
}

// fetchNamespacesByOwner returns all current namespaces belonging to the given user
// i.e., labeled with `"toolchain.dev.openshift.com/workspace":<workspace>`
// (see https://github.com/codeready-toolchain/host-operator/blob/master/deploy/templates/nstemplatetiers/appstudio/ns_appstudio.yaml)
func fetchNamespacesByWorkspace(cl runtimeclient.Client, workspace string) ([]corev1.Namespace, error) {
	// fetch all namespace with owner=username label
	namespaces := &corev1.NamespaceList{}
	if err := cl.List(context.TODO(), namespaces, listByWorkspaceLabel(workspace)); err != nil {
		return nil, err
	}
	return namespaces.Items, nil
}

// listByWorkspaceLabel returns runtimeclient.ListOption that filters by label toolchain.dev.openshift.com/workspace equal to the given workspace name
func listByWorkspaceLabel(w string) runtimeclient.ListOption {
	labels := map[string]string{toolchainv1alpha1.WorkspaceLabelKey: w}
	return runtimeclient.MatchingLabels(labels)
}
