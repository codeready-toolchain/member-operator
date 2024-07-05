package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"k8s.io/utils/strings/slices"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"k8s.io/apimachinery/pkg/api/resource"

	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestClusterResourceKinds(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	ctx := context.TODO()

	for _, clusterResourceKind := range clusterResourceKinds {
		johnyRuntimeObject := clusterResourceKind.object.DeepCopyObject()
		johnyObject, ok := johnyRuntimeObject.(client.Object)
		require.True(t, ok)
		johnyObjectLabels := map[string]string{toolchainv1alpha1.SpaceLabelKey: "johny"}
		johnyObject.SetLabels(johnyObjectLabels)
		johnyObject.SetName("johny-object")

		johnyRuntimeObject2 := clusterResourceKind.object.DeepCopyObject()
		johnyObject2, ok := johnyRuntimeObject2.(client.Object)
		require.True(t, ok)
		johnyObject2.SetLabels(johnyObjectLabels)
		johnyObject2.SetName("johny-object-2")

		anotherRuntimeObject := clusterResourceKind.object.DeepCopyObject()
		anotherObject, ok := anotherRuntimeObject.(client.Object)
		require.True(t, ok)
		anotherObject.SetLabels(map[string]string{toolchainv1alpha1.SpaceLabelKey: "another"})
		anotherObject.SetName("another-object")
		namespace := newNamespace("basic", "johny", "code")

		apiGroups := newAPIGroups(newAPIGroup("apps", "v1"), newAPIGroup("", "v1"), newAPIGroup(clusterResourceKind.gvk.Group, clusterResourceKind.gvk.Version))

		t.Run("listExistingResourcesIfAvailable should return one resource of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny", apiGroups)

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 1)
			assert.Equal(t, johnyObject, existingResources[0])
		})

		t.Run("listExistingResourcesIfAvailable should return two resources of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, johnyObject2, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny", apiGroups)

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 2)
			assert.Equal(t, johnyObject, existingResources[0])
			assert.Equal(t, johnyObject2, existingResources[1])
		})

		t.Run("listExistingResourcesIfAvailable should return not return any resource of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny", apiGroups)

			// then
			require.NoError(t, err)
			assert.Empty(t, existingResources)
		})

		t.Run("listExistingResourcesIfAvailable should return an error when listing resources of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject)
			fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny", apiGroups)

			// then
			require.Error(t, err)
			assert.Empty(t, existingResources)
		})

		t.Run("listExistingResourcesIfAvailable should not return any resource when APIGroup is missing for gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, johnyObject2, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny",
				newAPIGroups(newAPIGroup("apps", "v1"), newAPIGroup("", "v1")))

			// then
			require.NoError(t, err)
			assert.Empty(t, existingResources)
		})

		t.Run("listExistingResourcesIfAvailable should not return any resource when APIGroup is present but is missing the version "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, johnyObject2, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResourcesIfAvailable(ctx, fakeClient, "johny",
				newAPIGroups(newAPIGroup("apps", "v1"), newAPIGroup("", "v1"), newAPIGroup(clusterResourceKind.gvk.Group, "old")))

			// then
			require.NoError(t, err)
			assert.Empty(t, existingResources)
		})
	}

	t.Run("verify ClusterResourceQuota is in clusterResourceKinds", func(t *testing.T) {
		// given
		clusterResource := clusterResourceKinds[0]

		// then
		assert.Equal(t, &quotav1.ClusterResourceQuota{}, clusterResource.object)
		assert.Equal(t, quotav1.GroupVersion.WithKind("ClusterResourceQuota"), clusterResource.gvk)
	})

	t.Run("verify ClusterRoleBinding is in clusterResourceKinds", func(t *testing.T) {
		// given
		clusterResource := clusterResourceKinds[1]

		// then
		assert.Equal(t, &rbacv1.ClusterRoleBinding{}, clusterResource.object)
		assert.Equal(t, rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"), clusterResource.gvk)
	})

	t.Run("verify Idler is in clusterResourceKinds", func(t *testing.T) {
		// given
		clusterResource := clusterResourceKinds[2]

		// then
		assert.Equal(t, &toolchainv1alpha1.Idler{}, clusterResource.object)
		assert.Equal(t, toolchainv1alpha1.GroupVersion.WithKind("Idler"), clusterResource.gvk)
	})
}

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

				t.Run("first iteration - create first resource with no feature", func(t *testing.T) {
					// when

					// Each iteration of ensure() applies a single object only (if any) and exists after that.
					// Assuming that the controller would watch the created resources and would trigger another reconcile/ensure()
					// to apply all the objects one by one.
					// So the first iteration always creates the first ClusterResourceQuota with no feature only.
					// Even if the features are enabled.
					createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

					// then
					require.NoError(t, err)
					assert.True(t, createdOrUpdated)
					AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
						HasFinalizer().
						HasConditions(Provisioning())
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
						HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
						HasNoResource("feature-1-for-"+spacename, &quotav1.ClusterResourceQuota{}).
						HasNoResource("feature-2-for-"+spacename, &quotav1.ClusterResourceQuota{}).
						HasNoResource("feature-3-for-"+spacename, &quotav1.ClusterResourceQuota{})

					// Now, do another iteration and make sure the corresponding objects are created for each enabled feature
					enabledFeatures := strings.Split(testRun.enabledFeatures, ",")
					expectedFeatureToBeAlreadyCreated := make([]string, 0)
					for _, feature := range enabledFeatures {
						expectedFeatureToBeAlreadyCreated = append(expectedFeatureToBeAlreadyCreated, feature)
						t.Run(fmt.Sprintf("next iteration - create resource for feature %s if enabled", feature), func(t *testing.T) {
							// when
							createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

							// then
							require.NoError(t, err)
							assert.True(t, createdOrUpdated)
							AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
								HasFinalizer().
								HasConditions(Provisioning())
							var clusterAssertion *ClusterAssertion
							if testRun.enabledFeatures == "" {
								clusterAssertion = AssertThatCluster(t, fakeClient).
									HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
									HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
									HasNoResource("feature-1-for-"+spacename, &quotav1.ClusterResourceQuota{}).
									HasNoResource("feature-2-for-"+spacename, &quotav1.ClusterResourceQuota{}).
									HasNoResource("feature-3-for-"+spacename, &quotav1.ClusterResourceQuota{})
							} else {
								clusterAssertion = AssertThatCluster(t, fakeClient).
									HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
									HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{})
								// Check all expected features which should be already created by this and previous iterations
								for _, expectedFeature := range expectedFeatureToBeAlreadyCreated {
									clusterAssertion.HasResource(fmt.Sprintf("%s-for-%s", expectedFeature, spacename), &quotav1.ClusterResourceQuota{})
								}
								// Check that the rest of the features are not (yet) created
								for _, tierFeature := range allTierFeatures {
									if !slices.Contains(expectedFeatureToBeAlreadyCreated, tierFeature) {
										clusterAssertion.HasNoResource(fmt.Sprintf("%s-for-%s", tierFeature, spacename), &quotav1.ClusterResourceQuota{})
									}
								}
							}
						})
					}
				})
			})
		}
	})

	t.Run("should not create ClusterResource objects when the field is nil", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasNoConditions()
	})

	t.Run("should create only one CRQ when the template contains two CRQs", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
			HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("should create the second CRQ when the first one is already created but still not ClusterRoleBinding", func(t *testing.T) {
			// when
			createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, createdOrUpdated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer().
				HasConditions(Provisioning())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
				HasResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("should create ClusterRoleBinding when both CRQs are created", func(t *testing.T) {
				// when
				createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasConditions(Provisioning())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource("for-empty", &quotav1.ClusterResourceQuota{}).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})
	})

	t.Run("should not do anything when all cluster resources are already created", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"), withConditions(Provisioned()))
		crq := newClusterResourceQuota(spacename, "advanced")
		crb := newTektonClusterRoleBinding(spacename, "advanced")
		idlerDev := newIdler(spacename, spacename+"-dev", "advanced")
		idlerStage := newIdler(spacename, spacename+"-stage", "advanced")
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, idlerDev, idlerStage)

		// when
		createdOrUpdated, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
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

	t.Run("fail to list cluster resources", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("unable to list cluster resources")
		}

		// when
		_, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to list cluster resources")
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources("unable to list cluster resources"))
	})

	t.Run("fail to get template containing cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "fail", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		_, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve TierTemplate for the cluster resources with the name 'fail-clusterresources-abcde11'")
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
		_, err := manager.ensure(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create missing cluster resource of GVK 'quota.openshift.io/v1, Kind=ClusterResourceQuota'")
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"failed to apply cluster resource of type 'quota.openshift.io/v1, Kind=ClusterResourceQuota': unable to create resource of kind: ClusterResourceQuota, version: v1: unable to create resource of kind: ClusterResourceQuota, version: v1: some error"))
	})
}

func TestDeleteClusterResources(t *testing.T) {

	// given
	logger := zap.New(zap.UseDevMode(true))
	log.SetLogger(logger)
	ctx := log.IntoContext(context.TODO(), logger)
	spacename := "johnsmith"
	namespaceName := "toolchain-member"
	crq := newClusterResourceQuota(spacename, "advanced")
	crb := newTektonClusterRoleBinding(spacename, "advanced")
	nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "code"), withDeletionTs(), withClusterResources("abcde11"))

	t.Run("delete only ClusterResourceQuota", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

		// when
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("delete ClusterRoleBinding since CRQ is already deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})

	t.Run("should delete only one ClusterResourceQuota even when tier contains more ", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(spacename, "withemptycrq")
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		emptyCrq.Labels[toolchainv1alpha1.SpaceLabelKey] = spacename
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq, crb)

		// when
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
			HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("delete the for-empty CRQ since it's the last one to be deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})

	t.Run("delete the second ClusterResourceQuota since the first one has deletion timestamp set", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(spacename, "withemptycrq")
		deletionTS := metav1.NewTime(time.Now())
		crq.SetDeletionTimestamp(&deletionTS)
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		emptyCrq.Labels[toolchainv1alpha1.SpaceLabelKey] = spacename
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq)

		// when
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
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
			withNSTemplateSetFeatureAnnotation("feature-2"))
		crq := newClusterResourceQuota(spacename, "advanced", withFeatureAnnotation("feature-2"), withName("feature-2-for-"+spacename))

		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

		// when
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource(crq.Name, &quotav1.ClusterResourceQuota{})
	})

	t.Run("should not do anything when there is nothing to be deleted", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, deleted)
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
		deleted, err := manager.delete(ctx, nsTmplSet)

		// then
		require.Error(t, err)
		assert.False(t, deleted)
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(spacename, "advanced")

	t.Run("success", func(t *testing.T) {

		t.Run("upgrade from advanced to team tier by changing only the CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "team", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			codeNs := newNamespace("advanced", spacename, "code")
			crq := newClusterResourceQuota(spacename, "advanced")
			crb := newTektonClusterRoleBinding(spacename, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "team-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "team"),
					Containing(`"limits.cpu":"4","limits.memory":"15Gi"`)).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))

			t.Run("upgrade from advanced to team tier by changing only the CRB since CRQ is already changed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "team"),
						Containing(`"limits.cpu":"4","limits.memory":"15Gi"`)).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/tier", "team"))
			})
		})

		t.Run("upgrade from base to advanced tier by changing only the tier label - the templateref label doesn't change", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			codeNs := newNamespace("advanced", spacename, "code")
			crq := newClusterResourceQuota(spacename, "advanced")
			crq.Labels["toolchain.dev.openshift.com/tier"] = "base"
			crq.Spec.Quota.Hard["limits.cpu"] = resource.MustParse("100m")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`))
		})

		t.Run("promote from withemptycrq to advanced tier by removing the redundant CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("dev"), withClusterResources("abcde11"))
			codeNs := newNamespace("advanced", spacename, "code")
			crq := newClusterResourceQuota(spacename, "withemptycrq")
			crb := newTektonClusterRoleBinding(spacename, "withemptycrq")
			emptyCrq := newClusterResourceQuota(spacename, "withemptycrq")
			emptyCrq.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, emptyCrq, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq")).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq"))

			t.Run("promote from withemptycrq to advanced tier by changing only the CRQ since redundant CRQ is already removed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq"))

			})
		})

		t.Run("downgrade from advanced to basic tier by removing CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			crq := newClusterResourceQuota(spacename, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}). // no cluster resources in 'basic` tier
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("downgrade from advanced to basic tier by removing CRB since CRQ is already removed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}). // no cluster resources in 'basic` tier
					HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})

		t.Run("delete redundant cluster resources when ClusterResources field is nil in NSTemplateSet", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "withemptycrq") // no cluster resources, so the "advancedCRQ" should be deleted even if the tier contains the "advancedCRQ"
			crq := newClusterResourceQuota(spacename, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}). // resources were deleted
				HasNoResource("tekton-view-for-"+spacename, &rbacv1.ClusterRole{}).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("upgrade from basic to advanced by creating only CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withClusterResources("abcde11"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Provisioning())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("upgrade from basic to advanced by creating CRB since CRQ is already created", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Provisioning())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
						Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
			})
		})

		t.Run("upgrade from team to advanced with enabled features by updating existing CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spacename,
				"advanced",
				withClusterResources("abcde11"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			devNs := newNamespace("team", spacename, "dev")
			crq := newClusterResourceQuota(spacename, "team")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, devNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
				HasNoResource("feature-1-for-"+spacename, &quotav1.ClusterResourceQuota{})

			t.Run("upgrade from team to advanced by creating featured CRQ since regular CRQ is already created", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource("feature-1-for-"+spacename, &quotav1.ClusterResourceQuota{})
			})
		})

		t.Run("downgrade from advanced to team with featured CRQ to be deleted", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spacename,
				"team",
				withClusterResources("abcde11"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			devNs := newNamespace("advanced", spacename, "dev")
			crq := newClusterResourceQuota(spacename, "advanced", withFeatureAnnotation("feature-1"), withName("feature-1-for-"+spacename))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, devNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource(crq.Name, &quotav1.ClusterResourceQuota{})
		})

		t.Run("with another user", func(t *testing.T) {
			// given
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(spacename, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			anotherCrb := newTektonClusterRoleBinding("another", "basic")

			idlerDev := newIdler(spacename, spacename+"-dev", "advanced")
			idlerStage := newIdler(spacename, spacename+"-stage", "advanced")
			anotherIdlerDev := newIdler("another", "another-dev", "advanced")
			anotherIdlerStage := newIdler("another", "another-stage", "advanced")

			t.Run("no redundant cluster resources to be deleted for the given user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withConditions(Provisioned()), withClusterResources("abcde11"))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb, idlerDev, idlerStage, anotherIdlerDev, anotherIdlerStage)

				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.False(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Provisioned())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{}).
					HasResource("another-dev", &toolchainv1alpha1.Idler{}).
					HasResource("another-stage", &toolchainv1alpha1.Idler{})
			})

			t.Run("cluster resources should be deleted since it doesn't contain clusterResources template", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withConditions(Provisioned()))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb)

				// when - let remove everything
				var err error
				updated := true
				for ; updated; updated, err = manager.ensure(ctx, nsTmplSet) {
					require.NoError(t, err)
				}

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{})

			})
		})

		t.Run("delete only one redundant cluster resource during one call", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic") // no cluster resources, so the "advancedCRQ" should be deleted
			advancedCRQ := newClusterResourceQuota(spacename, "withemptycrq")
			anotherCRQ := newClusterResourceQuota(spacename, "withemptycrq")
			crb := newTektonClusterRoleBinding(spacename, "withemptycrq")
			anotherCRQ.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, advancedCRQ, anotherCRQ, crb)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(Updating()) //
			quotas := &quotav1.ClusterResourceQuotaList{}
			err = cl.List(context.TODO(), quotas, &client.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, quotas.Items, 1)
			AssertThatCluster(t, cl).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("it should delete the second for-empty CRQ since it's the last one", func(t *testing.T) {
				// when - should delete the second ClusterResourceQuota
				updated, err = manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				err = cl.List(context.TODO(), quotas, &client.ListOptions{})
				require.NoError(t, err)
				assert.Empty(t, quotas.Items)
				AssertThatCluster(t, cl).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

				t.Run("it should delete the CRB since both CRQs are already removed", func(t *testing.T) {
					// when - should delete the second ClusterResourceQuota
					updated, err = manager.ensure(ctx, nsTmplSet)

					// then
					require.NoError(t, err)
					assert.True(t, updated)
					err = cl.List(context.TODO(), quotas, &client.ListOptions{})
					require.NoError(t, err)
					assert.Empty(t, quotas.Items)
					roleBindings := &rbacv1.ClusterRoleBindingList{}
					err = cl.List(context.TODO(), roleBindings, &client.ListOptions{})
					require.NoError(t, err)
					assert.Empty(t, roleBindings.Items)
				})
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("promotion to another tier fails because it cannot list current resources", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev"), withConditions(Updating()))
			crq := newClusterResourceQuota(spacename, "fail")
			crb := newTektonClusterRoleBinding(spacename, "fail")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			_, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "fail-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "fail")).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "fail-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "fail"))
		})

		t.Run("fail to downgrade from advanced to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev"))
			crq := newClusterResourceQuota(spacename, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("some error")
			}

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"failed to delete an existing redundant cluster resource of name 'for-johnsmith' and gvk 'quota.openshift.io/v1, Kind=ClusterResourceQuota': some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(spacename, "advanced")
	crq := newClusterResourceQuota(spacename, "advanced")

	t.Run("success", func(t *testing.T) {

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde12", "dev"), withClusterResources("abcde12"))
			codeNs := newNamespace("advanced", spacename, "dev")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12")).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))

			t.Run("update from abcde11 revision to abcde12 revision by deleting CRB since CRQ is already changed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12")).
					HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier by deleting featured CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spacename,
				"advanced",
				withNamespaces("abcde12", "dev"),
				withClusterResources("abcde12"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			codeNs := newNamespace("advanced", spacename, "dev")
			crqFeatured := newClusterResourceQuota(spacename, "advanced", withName("feature-1-for-"+spacename), withFeatureAnnotation("feature-1"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{})
		})

		t.Run("update from abcde12 revision to abcde11 by restoring featured CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName,
				spacename,
				"advanced",
				withNamespaces("abcde11", "dev"),
				withClusterResources("abcde11"),
				withNSTemplateSetFeatureAnnotation("feature-1"))
			codeNs := newNamespace("advanced", spacename, "dev")
			crqFeatured := newClusterResourceQuota(spacename,
				"advanced",
				withName("feature-1-for-"+spacename),
				withTemplateRefUsingRevision("abcde12"),
				withFeatureAnnotation("feature-1"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
		})

		t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			crq := newClusterResourceQuota(spacename, "advanced", withTemplateRefUsingRevision("abcde12"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier by creating CRB", func(t *testing.T) {
				// when
				updated, err := manager.ensure(ctx, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("update to abcde11 fails because it cannot list current resources", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withClusterResources("abcde11"), withConditions(Updating()))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			_, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("update to abcde13 fails because it find the template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withClusterResources("abcde13"), withConditions(Updating()))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

			// when
			updated, err := manager.ensure(ctx, nsTmplSet)

			// then
			require.Error(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, spacename, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'advanced-clusterresources-abcde13' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"advanced-clusterresources-abcde13\" not found"))
			AssertThatCluster(t, cl).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	nsTmplSet := newNSTmplSet(namespaceName,
		spacename,
		"advanced",
		withNamespaces("abcde11", "dev"),
		withClusterResources("abcde11")) // The NSTemplateSet does not have the feature annotation (anymore)
	codeNs := newNamespace("advanced", spacename, "dev")
	crqFeatured := newClusterResourceQuota(spacename, "advanced", withName("feature-1-for-"+spacename), withFeatureAnnotation("feature-1"))
	manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crqFeatured, codeNs)

	// when
	updated, err := manager.ensure(ctx, nsTmplSet)

	// then
	require.NoError(t, err)
	assert.True(t, updated)
	AssertThatNSTemplateSet(t, namespaceName, spacename, cl).HasConditions(Updating())
	AssertThatCluster(t, cl).
		HasNoResource(crqFeatured.Name, &quotav1.ClusterResourceQuota{}) // The featured object is now deleted because the feature was disabled in the NSTemplateSet
}

func TestRetainObjectsOfSameGVK(t *testing.T) {
	// given
	clusterRole := runtime.RawExtension{Object: &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "ClusterRole",
			"apiVersion": "rbac.authorization.k8s.io/v1",
		}}}

	namespace := runtime.RawExtension{Object: &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "Namespace",
			"apiVersion": "v1",
		}}}
	clusterResQuota := runtime.RawExtension{Object: &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "ClusterResourceQuota",
			"apiVersion": "quota.openshift.io/v1",
		}}}
	clusterRoleBinding := runtime.RawExtension{Object: &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "ClusterRoleBinding",
			"apiVersion": "rbac.authorization.k8s.io/v1",
		}}}

	t.Run("verify retainObjectsOfSameGVK function for ClusterRole", func(t *testing.T) {
		// given
		retain := retainObjectsOfSameGVK(rbacv1.SchemeGroupVersion.WithKind("ClusterRole"))

		t.Run("should return false since the GVK doesn't match", func(t *testing.T) {
			for _, obj := range []runtime.RawExtension{namespace, clusterResQuota, clusterRoleBinding} {

				// when
				ok := retain(obj)

				// then
				assert.False(t, ok)

			}
		})

		t.Run("should return true since the GVK matches", func(t *testing.T) {

			// when
			ok := retain(clusterRole)

			// then
			assert.True(t, ok)
		})
	})
}
