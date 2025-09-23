package nstemplateset

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/strings/slices"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"k8s.io/apimachinery/pkg/api/resource"

	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestEnsureClusterResourcesOK(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spacename := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("should set status to provisioning", func(t *testing.T) {
		// given
		manager, failingClient := prepareClusterResourcesManager(t, nsTmplSet)

		err := manager.ensure(ctx, nsTmplSet)

		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, spacename, failingClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, failingClient).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
			HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
			HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{})
	})

	t.Run("should create only CRQs and set status to provisioning", func(t *testing.T) {
		tests := []struct {
			name            string
			enabledFeatures string
		}{
			{
				name:            "no enabled features",
				enabledFeatures: "",
			},
			{
				name:            "feature-1 enabled",
				enabledFeatures: "feature-1",
			},
			{
				name:            "feature-1 and feature-2 enabled",
				enabledFeatures: "feature-1,feature-2",
			},
			{
				name:            "feature-1 and feature-3 enabled",
				enabledFeatures: "feature-1,feature-3",
			},
			{
				name:            "feature-2 enabled",
				enabledFeatures: "feature-2",
			},
			{
				name:            "all features enabled",
				enabledFeatures: "feature-1,feature-2,feature-3",
			},
		}
		for _, testRun := range tests {
			t.Run(testRun.name, func(t *testing.T) {
				// given

				// Create a NSTemplate referring to a tier with four ClusterResourceQuota objects
				// The first one is with no feature annotation.
				// The other three represent feature-1, feature-2 and feature-3.
				allTierFeatures := []string{"feature-1", "feature-2", "feature-3"}
				if testRun.enabledFeatures != "" {
					nsTmplSet.Annotations = map[string]string{
						toolchainv1alpha1.FeatureToggleNameAnnotationKey: testRun.enabledFeatures,
					}
				}
				manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

				// when
				err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasConditions(Provisioning())

				// check that the resources not guarded by a feature gate are there
				clusterAssertion := AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{})

				// check that only the resources for the enabled features were deployed
				enabledFeatures := strings.Split(testRun.enabledFeatures, ",")
				for _, f := range allTierFeatures {
					if slices.Contains(enabledFeatures, f) {
						clusterAssertion.HasResource(fmt.Sprintf("%s-for-%s", f, spacename), &quotav1.ClusterResourceQuota{})
					} else {
						clusterAssertion.HasNoResource(fmt.Sprintf("%s-for-%s", f, spacename), &quotav1.ClusterResourceQuota{})
					}
				}
			})
		}
	})

	t.Run("should not create ClusterResource objects when the field is nil", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		err := manager.ensure(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasNoConditions()
	})

	t.Run("should not do anything when all cluster resources are already created", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced",
			withNamespaces("abcde11", "dev"),
			withClusterResources("abcde11"),
			withStatusClusterResources("abcde11"),
			withConditions(Provisioned()))
		crq := newClusterResourceQuota(spacename, "advanced")
		crb := newTektonClusterRoleBinding(spacename, "advanced")
		idlerDev := newIdler(spacename, spacename+"-dev", "advanced")
		idlerStage := newIdler(spacename, spacename+"-stage", "advanced")
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, idlerDev, idlerStage)

		// when
		err := manager.ensure(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
			HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
			HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{})
	})
}

func TestEnsureClusterResourcesFail(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spacename := "johnsmith-space"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("fail to get template containing cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "fail", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		err := manager.ensure(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve the TierTemplate for the to-be-applied cluster resources with the name 'fail-clusterresources-abcde11'")
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"unable to retrieve the TierTemplate 'fail-clusterresources-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"fail-clusterresources-abcde11\" not found"))
	})

	t.Run("fail to create cluster resources", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		err := manager.ensure(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "some error")
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"failed to apply changes to the cluster resource for-johnsmith-space, quota.openshift.io/v1, Kind=ClusterResourceQuota: failed to apply cluster resource: unable to create resource of kind: ClusterResourceQuota, version: v1: unable to create resource of kind: ClusterResourceQuota, version: v1: some error"))
	})
}

func TestDeleteClusterResources(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)
	spacename := "johnsmith"
	namespaceName := "toolchain-member"
	crq := newClusterResourceQuota(spacename, "advanced")
	crb := newTektonClusterRoleBinding(spacename, "advanced")
	nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "code"), withDeletionTs(), withClusterResources("abcde11"), withStatusClusterResources("abcde11"))

	t.Run("deletes all cluster resources", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

		// when
		err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
	})

	t.Run("delete the second ClusterResourceQuota since the first one has deletion timestamp set", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"), withStatusClusterResources("abcde11"))
		crq := newClusterResourceQuota(spacename, "withemptycrq", withFinalizer())
		deletionTS := metav1.NewTime(time.Now())
		crq.SetDeletionTimestamp(&deletionTS)
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		emptyCrq.Labels[toolchainv1alpha1.SpaceLabelKey] = spacename
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq)

		// when
		err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatCluster(t, cl).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}, HasDeletionTimestamp()).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{})
	})

	t.Run("delete ClusterResourceQuota for enabled feature", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName,
			spacename,
			"advanced",
			withNamespaces("abcde11", "dev", "code"),
			withDeletionTs(),
			withClusterResources("abcde11"),
			withStatusClusterResources("abcde11"),
			withNSTemplateSetFeatureAnnotation("feature-2"))
		crq := newClusterResourceQuota(spacename, "advanced", withFeatureAnnotation("feature-2"), withName("feature-2-for-"+spacename))

		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

		// when
		err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatCluster(t, cl).
			HasNoResource(crq.Name, &quotav1.ClusterResourceQuota{})
	})

	t.Run("should not do anything when there is nothing to be deleted", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{})
	})

	t.Run("failed to delete CRQ", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
		cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("mock error")
		}

		// when
		err := manager.delete(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Equal(t, "failed to delete cluster resource 'for-johnsmith': mock error", err.Error())
		AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
			HasFinalizer(). // finalizer was not added and nothing else was done
			HasConditions(UnableToTerminate("mock error"))
	})
}

func TestPromoteClusterResources(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spaceName := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(spaceName, "advanced")

	t.Run("success", func(t *testing.T) {
		t.Run("upgrade from advanced to team tier by changing only the CRQ", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(emptyCrq),
					test.WithParams(spacename),
				), "advanced", "clusterresources", "previousrevision")
			require.NoError(t, err)

			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "team",
				withNamespaces("abcde11", "dev"),
				withClusterResources("abcde11"),
				withStatusClusterResourcesInTier("advanced", "previousrevision"))
			codeNs := newNamespace("advanced", spaceName, "code")
			crq := newClusterResourceQuota(spaceName, "advanced")
			emptyCrq := newClusterResourceQuota("empty", "advanced")
			crb := newTektonClusterRoleBinding(spaceName, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs, previousTierTemplate, emptyCrq)

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
				HasNoResource("for-empty", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("promote from 1 tier to another", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq, clusterTektonRb, emptyCrq),
					test.WithParams(spacename),
				), "withemptycrq", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced",
				withNamespaces("dev"),
				withClusterResources("abcde11"),
				withStatusClusterResourcesInTier("withemptycrq", "previousrevision"))
			codeNs := newNamespace("advanced", spaceName, "code")
			crq := newClusterResourceQuota(spaceName, "withemptycrq")
			crq.Labels["disappearingLabel"] = "value"
			crq.Spec.Quota.Hard["limits.cpu"] = resource.MustParse("100m")
			crb := newTektonClusterRoleBinding(spaceName, "withemptycrq")
			crb.Labels["disappearingLabel"] = "value"
			emptyCrq := newClusterResourceQuota(spaceName, "withemptycrq")
			emptyCrq.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, emptyCrq, crq, crb, codeNs, previousTierTemplate)

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{},
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`),
					WithoutLabel("disappearingLabel")).
				HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithoutLabel("disappearingLabel"))
		})

		t.Run("promoting to no tier (nil cluster resources in spec) deletes all cluster resources", func(t *testing.T) {
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq, clusterTektonRb),
					test.WithParams(spacename),
				), "advanced", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "withemptycrq", withStatusClusterResourcesInTier("advanced", "previousrevision"))
			crq := newClusterResourceQuota(spaceName, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, previousTierTemplate)

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}). // resources were deleted
				HasNoResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("promote to a new tier with a new feature enabled", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq, clusterTektonRb),
					test.WithParams(spacename),
				), "team", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName,
				spaceName,
				"advanced",
				withClusterResources("abcde11"),
				withStatusClusterResourcesInTier("team", "previousrevision"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			devNs := newNamespace("team", spaceName, "dev")
			crq := newClusterResourceQuota(spaceName, "team")
			crq.Spec.Quota.Hard["limits.cpu"] = resource.MustParse("100m")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, devNs, previousTierTemplate)

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{},
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
				HasResource("feature-1-for-"+spaceName, &quotav1.ClusterResourceQuota{})
		})

		t.Run("promote to a new tier with feature enabled", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq, clusterTektonRb, crqFeature1),
					test.WithParams(spacename),
				), "advanced", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName,
				spaceName,
				"team",
				withClusterResources("abcde11"),
				withStatusClusterResourcesInTier("advanced", "previousrevision"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			devNs := newNamespace("advanced", spaceName, "dev")
			crq := newClusterResourceQuota(spaceName, "advanced", withFeatureAnnotation("feature-1"), withName("feature-1-for-"+spaceName))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, devNs, previousTierTemplate)

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource(crq.Name, &quotav1.ClusterResourceQuota{})
		})

		t.Run("with another user", func(t *testing.T) {
			// given
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(spaceName, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			anotherCrb := newTektonClusterRoleBinding("another", "basic")

			idlerDev := newIdler(spaceName, spaceName+"-dev", "advanced")
			idlerStage := newIdler(spaceName, spaceName+"-stage", "advanced")
			anotherIdlerDev := newIdler("another", "another-dev", "advanced")
			anotherIdlerStage := newIdler("another", "another-stage", "advanced")

			t.Run("no redundant cluster resources to be deleted for the given user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withConditions(Provisioned()), withClusterResources("abcde11"), withStatusClusterResources("abcde11"))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb, idlerDev, idlerStage, anotherIdlerDev, anotherIdlerStage)

				// when
				err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
					HasFinalizer().
					HasConditions(Provisioned())
				AssertThatCluster(t, cl).
					HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(spaceName+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(spaceName+"-stage", &toolchainv1alpha1.Idler{}).
					HasResource("another-dev", &toolchainv1alpha1.Idler{}).
					HasResource("another-stage", &toolchainv1alpha1.Idler{})
			})

			t.Run("promoting to no tier should delete only the resources belonging to the nstemplateset", func(t *testing.T) {
				// given
				previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
					test.CreateTemplate(
						test.WithObjects(advancedCrq, clusterTektonRb, crqFeature1),
						test.WithParams(spacename),
					), "advanced", "clusterresources", "previousrevision")
				require.NoError(t, err)
				nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withConditions(Provisioned()), withStatusClusterResources("previousrevision"))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb, previousTierTemplate)

				err = manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
					HasNoResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})
	})

	t.Run("failure", func(t *testing.T) {
		t.Run("promotion to another tier fails because it cannot create resources", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq),
					test.WithParams(spacename),
				), "fail", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced",
				withNamespaces("abcde11", "dev"),
				withConditions(Updating()),
				withClusterResources("abcde11"),
				withStatusClusterResourcesInTier("fail", "previousrevision"))
			crq := newClusterResourceQuota(spaceName, "fail")
			crb := newTektonClusterRoleBinding(spaceName, "fail")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, previousTierTemplate)
			cl.MockCreate = func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
				return fmt.Errorf("some error")
			}

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("failed to apply changes to the cluster resource johnsmith-dev, toolchain.dev.openshift.com/v1alpha1, Kind=Idler: failed to apply cluster resource: unable to create resource of kind: Idler, version: v1alpha1: unable to create resource of kind: Idler, version: v1alpha1: some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
				HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("fail to downgrade from advanced to basic tier", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq),
					test.WithParams(spacename),
				), "advanced", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "basic", withNamespaces("abcde11", "dev"), withStatusClusterResourcesInTier("advanced", "previousrevision"))
			crq := newClusterResourceQuota(spaceName, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, previousTierTemplate)
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("some error")
			}

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"failed to delete obsolete object 'for-johnsmith' of kind 'ClusterResourceQuota' in namespace '': some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
				HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})
}

func TestUpdateClusterResources(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spaceName := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(spaceName, "advanced")
	crq := newClusterResourceQuota(spaceName, "advanced")

	t.Run("success", func(t *testing.T) {
		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withNamespaces("abcde12", "dev"), withClusterResources("abcde12"), withStatusClusterResources("abcde11"))
			codeNs := newNamespace("advanced", spaceName, "dev")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
				HasNoResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier by deleting featured CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spaceName,
				"advanced",
				withNamespaces("abcde12", "dev"),
				withClusterResources("abcde12"),
				withStatusClusterResources("abcde11"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			codeNs := newNamespace("advanced", spaceName, "dev")
			crqFeatured := newClusterResourceQuota(spaceName, "advanced", withName("feature-1-for-"+spaceName), withFeatureAnnotation("feature-1"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs)

			// when
			err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{})
		})

		t.Run("update from abcde12 revision to abcde11 by restoring featured CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spaceName,
				"advanced",
				withNamespaces("abcde11", "dev"),
				withClusterResources("abcde11"),
				withStatusClusterResources("abcde12"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			codeNs := newNamespace("advanced", spaceName, "dev")
			crqFeatured := newClusterResourceQuota(spaceName,
				"advanced",
				withName("feature-1-for-"+spaceName),
				withTemplateRefUsingRevision("abcde12"),
				withFeatureAnnotation("feature-1"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs)

			// when
			err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
		})

		t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"), withStatusClusterResources("abcde12"))
			crq := newClusterResourceQuota(spaceName, "advanced", withTemplateRefUsingRevision("abcde12"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{}).
				HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})

	t.Run("failure", func(t *testing.T) {
		t.Run("update to abcde11 fails because it cannot list current resources", func(t *testing.T) {
			// given
			previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
				test.CreateTemplate(
					test.WithObjects(advancedCrq),
					test.WithParams(spacename),
				), "advanced", "clusterresources", "previousrevision")
			require.NoError(t, err)
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withClusterResources("abcde11"), withConditions(Updating()), withStatusClusterResources("previousrevision"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, previousTierTemplate)
			cl.MockCreate = func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
				return fmt.Errorf("some error")
			}

			// when
			err = manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("failed to apply changes to the cluster resource johnsmith-tekton-view, rbac.authorization.k8s.io/v1, Kind=ClusterRoleBinding: failed to apply cluster resource: unable to create resource of kind: ClusterRoleBinding, version: v1: unable to create resource of kind: ClusterRoleBinding, version: v1: some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasNoResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("update to abcde13 fails because it find the template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spaceName, "advanced", withClusterResources("abcde13"), withConditions(Updating()))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

			// when
			err := manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'advanced-clusterresources-abcde13' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"advanced-clusterresources-abcde13\" not found"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spaceName, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasResource(spaceName+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
		})
	})
}

func TestDeleteFeatureFromNSTemplateSet(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spaceName := "johnsmith"
	namespaceName := "toolchain-member"

	previousTierTemplate, err := createTierTemplate(scheme.Codecs.UniversalDeserializer(),
		test.CreateTemplate(
			test.WithObjects(advancedCrq, crqFeature1),
			test.WithParams(spacename),
		), "advanced", "clusterresources", "previousrevision")
	require.NoError(t, err)

	nsTmplSet := newNSTmplSet(namespaceName,
		spaceName,
		"advanced",
		withNamespaces("abcde11", "dev"),
		withClusterResources("abcde11"),
		withStatusClusterResources("previousrevision"),
	) // The NSTemplateSet does not have the feature annotation (anymore)
	codeNs := newNamespace("advanced", spaceName, "dev")
	crqFeatured := newClusterResourceQuota(spaceName, "advanced", withName("feature-1-for-"+spaceName), withFeatureAnnotation("feature-1"))
	manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs, previousTierTemplate)

	// when
	err = manager.ensure(ctx, nsTmplSet)

	// then
	require.NoError(t, err)
	AssertThatNSTemplateSet(t, namespaceName, spaceName, cl).HasConditions(Updating())
	AssertThatCluster(t, cl).
		HasNoResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{}) // The featured object is now deleted because the feature was disabled in the NSTemplateSet
}
