package nstemplateset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	. "github.com/codeready-toolchain/member-operator/test"
	commontest "github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestEnsureSpaceRoles(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	t.Run("success", func(t *testing.T) {

		t.Run("create admin and viewer roles and rolebindings", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio",
				withSpaceRoles(map[string][]string{
					"appstudio-admin-abcde11":  {"user1", "user2"},
					"appstudio-viewer-abcde11": {"user3", "user4"},
				}))
			ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "appstudio", // ns.name=oddity-appstudio
				withTemplateRefUsingRevision("abcde10"), // starting with an older revision
			)
			mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)

			// when
			createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, createdOrUpdated)
			// at this point, the NSTemplateSet is still in `updating` state
			AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
				HasConditions(Updating())
			// verify that roles and rolebindings were created
			AssertThatRole(t, "oddity-appstudio", "space-admin", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())
			AssertThatRoleBinding(t, "oddity-appstudio", "user1-space-admin", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())
			AssertThatRoleBinding(t, "oddity-appstudio", "user2-space-admin", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())
			AssertThatRole(t, "oddity-appstudio", "space-viewer", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())
			AssertThatRoleBinding(t, "oddity-appstudio", "user3-space-viewer", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())
			AssertThatRoleBinding(t, "oddity-appstudio", "user4-space-viewer", memberClient).
				Exists(). // created
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, nsTmplSet.GetName())

			t.Run("update annotation", func(t *testing.T) {
				// when
				_, err := mgr.ensure(logger, nsTmplSet)

				// then
				// verify that the `last-applied-space-roles` annotation was set on namespace
				require.NoError(t, err)
				lastApplied, err := json.Marshal(nsTmplSet.Spec.SpaceRoles)
				require.NoError(t, err)
				AssertThatNamespace(t, "oddity-appstudio", memberClient.Client).
					HasAnnotation(toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey, string(lastApplied))
			})

			t.Run("remove admin role and rolebindings", func(t *testing.T) {
				// given
				nsTmplSet.Spec.SpaceRoles = []toolchainv1alpha1.NSTemplateSetSpaceRole{
					{
						TemplateRef: "appstudio-viewer-abcde11",
						Usernames:   []string{"user3", "user4"},
					},
				}
				// when
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet) // precreate the resources for the initial set of SpaceRoles

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated) // outdated objs deleted
				// at this point, the NSTemplateSet is still in `updating` state
				AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
					HasConditions(Updating())

				// verify that `admin` role and rolebinding for `user1` and `user2` were deleted, but nothing changed for the viewer role and rolebindings
				AssertThatRole(t, "oddity-appstudio", "space-admin", memberClient).DoesNotExist()              // deleted
				AssertThatRoleBinding(t, "oddity-appstudio", "user1-space-admin", memberClient).DoesNotExist() // deleted
				AssertThatRoleBinding(t, "oddity-appstudio", "user2-space-admin", memberClient).DoesNotExist() // deleted
				AssertThatRole(t, "oddity-appstudio", "space-viewer", memberClient).Exists()                   // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user3-space-viewer", memberClient).Exists()      // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user4-space-viewer", memberClient).Exists()      // unchanged

			})
		})

		t.Run("update roles", func(t *testing.T) {

			t.Run("add admin user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "basic",
					withSpaceRoles(map[string][]string{
						"basic-admin-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "dev", // ns.name=oddity-dev
					withTemplateRefUsingRevision("abcde10"), // starting with an older revision
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet) // precreate the resources for the initial set of SpaceRoles
				require.NoError(t, err)
				// add `user3` in admin space roles
				nsTmplSet.Spec.SpaceRoles[0].Usernames = append(nsTmplSet.Spec.SpaceRoles[0].Usernames, "user3")

				// when
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated)
				// at this point, the NSTemplateSet is still in `updating` state
				AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
					HasConditions(Updating())
					// verify that role bindings for `user1` and `user2`` still exist and thet one was added for `user3`
				AssertThatRole(t, "oddity-dev", "space-admin", memberClient).Exists()              // unchanged
				AssertThatRoleBinding(t, "oddity-dev", "user1-space-admin", memberClient).Exists() // unchanged
				AssertThatRoleBinding(t, "oddity-dev", "user2-space-admin", memberClient).Exists() // unchanged
				AssertThatRoleBinding(t, "oddity-dev", "user3-space-admin", memberClient).Exists() // created
			})

			t.Run("remove admin user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "basic",
					withSpaceRoles(map[string][]string{
						"basic-admin-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "dev",
					withTemplateRefUsingRevision("abcde10"), // starting with an older revision
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet) // precreate the resources for the initial set of SpaceRoles
				require.NoError(t, err)

				// remove `user1` from admin space roles
				nsTmplSet.Spec.SpaceRoles[0].Usernames = []string{"user2"}

				// when
				changed, err := mgr.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, changed) // outdated objs deleted
				// at this point, the NSTemplateSet is still in `updating` state
				AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
					HasConditions(Updating())
				// verify that rolebinding for `user1` was removed but the one for `user2` was not changed
				AssertThatRole(t, "oddity-dev", "space-admin", memberClient).Exists()                    // unchanged
				AssertThatRoleBinding(t, "oddity-dev", "user1-space-admin", memberClient).DoesNotExist() // deleted
				AssertThatRoleBinding(t, "oddity-dev", "user2-space-admin", memberClient).Exists()       // unchanged
			})

			t.Run("no change", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "basic",
					withSpaceRoles(map[string][]string{
						"basic-admin-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "dev", // ns.name=oddity-dev
					withTemplateRefUsingRevision("abcde11"),
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet) // precreate the resources for the initial set of SpaceRoles
				require.NoError(t, err)

				// when calling without any change in the NSTemplateSet vs existing resources
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)
				require.NoError(t, err)

				// at this point, the NSTemplateSet is still in `updating` state
				AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
					HasConditions(Updating())
					// then verify that roles and rolebindings still exist, but nothing changed
				assert.False(t, createdOrUpdated)
				AssertThatRole(t, "oddity-dev", "space-admin", memberClient).Exists()              // created
				AssertThatRoleBinding(t, "oddity-dev", "user1-space-admin", memberClient).Exists() // created
				AssertThatRoleBinding(t, "oddity-dev", "user2-space-admin", memberClient).Exists() // created
			})
		})

	})

	t.Run("failures", func(t *testing.T) {

		t.Run("error while listing namespaces", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "basic", withNamespaces("abcde11", "basic"))
			mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet)
			memberClient.MockList = func(ctx context.Context, list runtimeclient.ObjectList, opts ...runtimeclient.ListOption) error {
				if _, ok := list.(*corev1.NamespaceList); ok {
					return fmt.Errorf("mock error")
				}
				return memberClient.Client.List(ctx, list, opts...)
			}

			// when
			_, err := mgr.ensure(logger, nsTmplSet)

			// then
			assert.EqualError(t, err, "failed to list namespaces for workspace 'oddity': mock error")
			// at this point, the NSTemplateSet is still in `updating` state
			AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
				HasConditions(UnableToProvision("mock error"))

		})

		t.Run("tiertemplate not found", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "unknown",
				withNamespaces("abcde11", "unknown"),
				withSpaceRoles(map[string][]string{ // at least 1 space role is needed here
					"admin-unknown-abcde11": {"user1", "user2"},
				}))
			ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "unknown")
			mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
			// when
			_, err := mgr.ensure(logger, nsTmplSet)

			// then
			assert.EqualError(t, err, "failed to retrieve space roles to apply: unable to retrieve the TierTemplate 'admin-unknown-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"admin-unknown-abcde11\" not found")
			AssertThatNSTemplateSet(t, commontest.MemberOperatorNs, "oddity", memberClient).
				HasConditions(UpdateFailed(`unable to retrieve the TierTemplate 'admin-unknown-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com "admin-unknown-abcde11" not found`))
		})

	})
}

func prepareSpaceRolesManager(t *testing.T, initObjs ...runtime.Object) (*spaceRolesManager, *commontest.FakeClient) {
	os.Setenv("WATCH_NAMESPACE", commontest.MemberOperatorNs)
	statusManager, fakeClient := prepareStatusManager(t, initObjs...)
	return &spaceRolesManager{
		statusManager: statusManager,
	}, fakeClient
}
