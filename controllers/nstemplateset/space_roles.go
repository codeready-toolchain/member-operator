package nstemplateset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
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
func (r *spaceRolesManager) ensure(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	logger = logger.WithValues("nstemplateset_name", nsTmplSet.Name)

	nss, err := fetchNamespacesByOwner(r.Client, nsTmplSet.Name)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
			"failed to list namespaces for workspace '%s'", nsTmplSet.Name)
	}
	logger.Info("ensuring space roles", "namespace_count", len(nss), "role_count", len(nsTmplSet.Spec.SpaceRoles))
	for _, ns := range nss {
		// space roles previously applied
		// read annotation to see what was applied last time, so we can compare with the new SpaceRoles and remove all obsolete resources (based on their kind/names)
		var lastAppliedSpaceRoles []toolchainv1alpha1.NSTemplateSetSpaceRole
		if currentSpaceRolesAnnotation, exists := ns.Annotations[toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey]; exists && currentSpaceRolesAnnotation != "" {
			if err := json.Unmarshal([]byte(currentSpaceRolesAnnotation), &lastAppliedSpaceRoles); err != nil {
				return false, errors.Wrap(err, "unable to decode current space roles in annotation")
			}
		}
		// compare last-applied vs spec to see if there's anything obsolete
		// note: we only set the NSTemplateSet status to `provisioning` if there are resource changes,
		// but for other cases (such as restoring resources deleted by a user), we don't set the NSTemplateSet status to `provisioning`.
		if !reflect.DeepEqual(nsTmplSet.Spec.SpaceRoles, lastAppliedSpaceRoles) {
			if err := r.setStatusUpdatingIfNotProvisioning(nsTmplSet); err != nil {
				return false, err
			}
		}
		lastAppliedSpaceRoleObjs, err := r.getSpaceRolesObjects(&ns, lastAppliedSpaceRoles) // nolint:gosec
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForSpaceRolesFailure(logger, nsTmplSet, err, "failed to retrieve last applied space roles")
		}
		// space roles to apply now
		spaceRoleObjs, err := r.getSpaceRolesObjects(&ns, nsTmplSet.Spec.SpaceRoles) // nolint:gosec
		if err != nil {
			return false, r.wrapErrorWithStatusUpdateForSpaceRolesFailure(logger, nsTmplSet, err, "failed to retrieve space roles to apply")
		}

		// labels to apply on all new objects
		var labels = map[string]string{
			toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
			toolchainv1alpha1.OwnerLabelKey:    nsTmplSet.GetName(),
			toolchainv1alpha1.SpaceLabelKey:    nsTmplSet.GetName(),
		}
		logger.Info("applying space role objects", "count", len(spaceRoleObjs))
		// create (or update existing) objects based the tier template
		if _, err = r.ApplyToolchainObjects(logger, spaceRoleObjs, labels); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s' with space roles", ns.Name)
		}

		if err := deleteObsoleteObjects(logger, r.Client, lastAppliedSpaceRoleObjs, spaceRoleObjs); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant objects in namespace '%s'", ns.Name)
		}

		if !reflect.DeepEqual(nsTmplSet.Spec.SpaceRoles, lastAppliedSpaceRoles) {
			// store the space roles in an annotation at the namespace level, so we know what was applied and how to deal with
			// diffs when the space roles are changed (users added or removed, etc.)
			sr, err := json.Marshal(nsTmplSet.Spec.SpaceRoles)
			if err != nil {
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
					fmt.Sprintf("failed to marshal space roles to update '%s' annotation on namespace", toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey))
			}
			if ns.Annotations == nil {
				ns.Annotations = map[string]string{}
			}
			ns.Annotations[toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey] = string(sr)
			if err := r.Client.Update(context.TODO(), &ns); err != nil { // nolint:gosec
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
					fmt.Sprintf("failed to update namespace with '%s' annotation", toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey))
			}
			logger.Info("updated annotation on namespace", toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey, string(sr))
			return true, nil
		}
	}
	return false, nil
}

// Get the space role objects from the templates specified in the given `spaceRoles`
// Returns the objects, or an error if something wrong happened when processing the templates
func (r *spaceRolesManager) getSpaceRolesObjects(ns *corev1.Namespace, spaceRoles []toolchainv1alpha1.NSTemplateSetSpaceRole) ([]runtimeclient.Object, error) {
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
				SpaceName: username,
			})
			if err != nil {
				return nil, errors.Wrapf(err, "failed to process space roles template '%s' for the user '%s' in namespace '%s'", spaceRole.TemplateRef, username, ns.Name)
			}
			spaceRoleObjects = append(spaceRoleObjects, objs...)
		}
	}
	return spaceRoleObjects, nil
}
