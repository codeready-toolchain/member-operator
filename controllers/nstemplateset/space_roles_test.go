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

		t.Run("create roles", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio",
				withSpaceRoles(map[string][]string{
					"admin-appstudio-abcde11":  {"user1", "user2"},
					"viewer-appstudio-abcde11": {"user3", "user4"},
				}))
			ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "appstudio", // ns.name=oddity=appstudio
				withTemplateRefUsingRevision("abcde10"), // starting with an older revision
				withWorkspaceLabel("oddity"))
			mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)

			// when
			createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, createdOrUpdated)
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
			// verify that the `last-applied-space-roles` annotation was set on namespace
			lastApplied, err := json.Marshal(nsTmplSet.Spec.SpaceRoles)
			require.NoError(t, err)
			AssertThatNamespace(t, "oddity-appstudio", memberClient.Client).
				HasAnnotation(toolchainv1alpha1.LastSpaceRolesAnnotationKey, string(lastApplied))
		})

		t.Run("update roles", func(t *testing.T) {

			t.Run("add admin user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio",
					withSpaceRoles(map[string][]string{
						"admin-appstudio-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "appstudio", // ns.name=oddity=appstudio
					withTemplateRefUsingRevision("abcde10"), // starting with an older revision
					withWorkspaceLabel("oddity"),
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet)
				require.NoError(t, err)
				// add `user3` in admin space roles
				nsTmplSet.Spec.SpaceRoles[0].Usernames = append(nsTmplSet.Spec.SpaceRoles[0].Usernames, "user3")

				// when
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated)
				// verify that role bindings for `user1` and `user2`` still exist and thet one was added for `user3`
				AssertThatRole(t, "oddity-appstudio", "space-admin", memberClient).Exists()              // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user1-space-admin", memberClient).Exists() // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user2-space-admin", memberClient).Exists() // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user3-space-admin", memberClient).Exists() // created
			})

			t.Run("remove admin user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio",
					withSpaceRoles(map[string][]string{
						"admin-appstudio-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "appstudio", // ns.name=oddity=appstudio
					withTemplateRefUsingRevision("abcde10"), // starting with an older revision
					withWorkspaceLabel("oddity"),
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet)
				require.NoError(t, err)
				// remove `user1` from admin space roles
				nsTmplSet.Spec.SpaceRoles[0].Usernames = []string{"user2"}

				// when
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated) // outdated objs deleted
				// verify that rolebinding for `user1` was removed but the one for `user2` was not changed
				AssertThatRole(t, "oddity-appstudio", "space-admin", memberClient).Exists()                    // unchanged
				AssertThatRoleBinding(t, "oddity-appstudio", "user1-space-admin", memberClient).DoesNotExist() // deleted
				AssertThatRoleBinding(t, "oddity-appstudio", "user2-space-admin", memberClient).Exists()       // unchanged
			})

			t.Run("no change", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio",
					withSpaceRoles(map[string][]string{
						"admin-appstudio-abcde11": {"user1", "user2"},
					}))
				ns := newNamespace(nsTmplSet.Spec.TierName, "oddity", "appstudio", // ns.name=oddity=appstudio
					withTemplateRefUsingRevision("abcde10"), // starting with an older revision
					withWorkspaceLabel("oddity"),
				)
				mgr, memberClient := prepareSpaceRolesManager(t, nsTmplSet, ns)
				_, err := mgr.ensure(logger, nsTmplSet)
				require.NoError(t, err)

				// when calling without any change in the NSTemplateSet vs existing resources
				createdOrUpdated, err := mgr.ensure(logger, nsTmplSet)
				require.NoError(t, err)

				// then verify that roles and rolebindings still exist, but nothing changed
				assert.False(t, createdOrUpdated)
				AssertThatRole(t, "oddity-appstudio", "space-admin", memberClient).Exists()              // created
				AssertThatRoleBinding(t, "oddity-appstudio", "user1-space-admin", memberClient).Exists() // created
				AssertThatRoleBinding(t, "oddity-appstudio", "user2-space-admin", memberClient).Exists() // created
			})
		})
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("error while listing namespaces", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "appstudio", withNamespaces("abcde11", "appstudio"))
			mgr, cl := prepareSpaceRolesManager(t, nsTmplSet)
			cl.MockList = func(ctx context.Context, list runtimeclient.ObjectList, opts ...runtimeclient.ListOption) error {
				if _, ok := list.(*corev1.NamespaceList); ok {
					return fmt.Errorf("mock error")
				}
				return cl.Client.List(ctx, list, opts...)
			}

			// when
			_, err := mgr.ensure(logger, nsTmplSet)

			// then
			assert.EqualError(t, err, "failed to list namespaces for workspace 'oddity': mock error")
		})

		t.Run("tiertemplate not found", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(commontest.MemberOperatorNs, "oddity", "unknown",
				withNamespaces("abcde11", "unknown"),
				withSpaceRoles(map[string][]string{ // at least 1 space role is needed here
					"admin-unknown-abcde11": {"user1", "user2"},
				}))
			ns := newNamespace(nsTmplSet.Spec.TierName, "oddity-unknown", "unknown", withWorkspaceLabel("oddity"))
			mgr, _ := prepareSpaceRolesManager(t, nsTmplSet, ns)
			// when
			_, err := mgr.ensure(logger, nsTmplSet)

			// then
			assert.EqualError(t, err, "failed to retrieve space roles to apply: unable to retrieve the TierTemplate 'admin-unknown-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"admin-unknown-abcde11\" not found")
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
