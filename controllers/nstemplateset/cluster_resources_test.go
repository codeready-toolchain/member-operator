package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var logger = ctrl.Log.WithName("controllers").WithName("NSTemplateSet")

func TestClusterResourceKinds(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	for _, clusterResourceKind := range clusterResourceKinds {
		johnyRuntimeObject := clusterResourceKind.object.DeepCopyObject()
		johnyObject, ok := johnyRuntimeObject.(client.Object)
		require.True(t, ok)
		johnyObject.SetLabels(map[string]string{"toolchain.dev.openshift.com/owner": "johny"})
		johnyObject.SetName("johny-object")

		johnyRuntimeObject2 := clusterResourceKind.object.DeepCopyObject()
		johnyObject2, ok := johnyRuntimeObject2.(client.Object)
		require.True(t, ok)
		johnyObject2.SetLabels(map[string]string{"toolchain.dev.openshift.com/owner": "johny"})
		johnyObject2.SetName("johny-object-2")

		anotherRuntimeObject := clusterResourceKind.object.DeepCopyObject()
		anotherObject, ok := anotherRuntimeObject.(client.Object)
		require.True(t, ok)
		anotherObject.SetLabels(map[string]string{"toolchain.dev.openshift.com/owner": "another"})
		anotherObject.SetName("another-object")
		namespace := newNamespace("basic", "johny", "code")

		t.Run("listExistingResources should return one resource of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResources(fakeClient, "johny")

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 1)
			assert.Equal(t, johnyObject, existingResources[0].GetClientObject())
		})

		t.Run("listExistingResources should return two resources of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, johnyObject2, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResources(fakeClient, "johny")

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 2)
			assert.Equal(t, johnyObject, existingResources[0].GetClientObject())
			assert.Equal(t, johnyObject2, existingResources[1].GetClientObject())
		})

		t.Run("listExistingResources should return not return any resource of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, namespace)

			// when
			existingResources, err := clusterResourceKind.listExistingResources(fakeClient, "johny")

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 0)
		})

		t.Run("listExistingResources should return an error when listing resources of gvk "+clusterResourceKind.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject)
			fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			existingResources, err := clusterResourceKind.listExistingResources(fakeClient, "johny")

			// then
			require.Error(t, err)
			require.Len(t, existingResources, 0)
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
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))

	t.Run("should create only CRQ and set status to provisioning", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
	})

	t.Run("should not create ClusterResource objects when the field is nil", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasNoConditions()
	})

	t.Run("should create only one CRQ when the template contains two CRQs", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
			HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("should create the second CRQ when the first one is already created but still not ClusterRoleBinding", func(t *testing.T) {
			// when
			createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, createdOrUpdated)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasConditions(Provisioning())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("should create ClusterRoleBinding when both CRQs are created", func(t *testing.T) {
				// when
				createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, createdOrUpdated)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioning())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
					HasResource("for-empty", &quotav1.ClusterResourceQuota{}).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})
	})

	t.Run("should not do anything when all cluster resources are already created", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"), withConditions(Provisioned()))
		crq := newClusterResourceQuota(username, "advanced")
		crb := newTektonClusterRoleBinding(username, "advanced")
		idlerDev := newIdler(username, username+"-dev")
		idlerCode := newIdler(username, username+"-code")
		idlerStage := newIdler(username, username+"-stage")
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, idlerDev, idlerCode, idlerStage)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
			HasResource(username+"-dev", &toolchainv1alpha1.Idler{}).
			HasResource(username+"-code", &toolchainv1alpha1.Idler{}).
			HasResource(username+"-stage", &toolchainv1alpha1.Idler{})
	})
}

func TestEnsureClusterResourcesFail(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))

	t.Run("fail to list cluster resources", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("unable to list cluster resources")
		}

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to list cluster resources")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources("unable to list cluster resources"))
	})

	t.Run("fail to get template containing cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "fail", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve TierTemplate for the cluster resources with the name 'fail-clusterresources-abcde11'")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
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
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create missing cluster resource of GVK 'quota.openshift.io/v1, Kind=ClusterResourceQuota'")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"failed to apply cluster resource of type 'quota.openshift.io/v1, Kind=ClusterResourceQuota'"))
	})
}

func TestDeleteClusterResources(t *testing.T) {

	username := "johnsmith"
	namespaceName := "toolchain-member"
	crq := newClusterResourceQuota(username, "advanced")
	crb := newTektonClusterRoleBinding(username, "advanced")
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev", "code"), withDeletionTs(), withClusterResources("abcde11"))

	t.Run("delete only ClusterResourceQuota", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

		// when
		deleted, err := manager.delete(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("delete ClusterRoleBinding since CRQ is already deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})

	t.Run("should delete only one ClusterResourceQuota even when tier contains more ", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(username, "withemptycrq")
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		emptyCrq.Labels["toolchain.dev.openshift.com/owner"] = username
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq, crb)

		// when
		deleted, err := manager.delete(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("delete the for-empty CRQ since it's the last one to be deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})
	})

	t.Run("delete the second ClusterResourceQuota since the first one has deletion timestamp set", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(username, "withemptycrq")
		deletionTS := metav1.NewTime(time.Now())
		crq.SetDeletionTimestamp(&deletionTS)
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		emptyCrq.Labels["toolchain.dev.openshift.com/owner"] = username
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq)

		// when
		deleted, err := manager.delete(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}, HasDeletionTimestamp()).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{})
	})

	t.Run("should not do anything when there is nothing to be deleted", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		deleted, err := manager.delete(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
	})

	t.Run("failed to delete CRQ", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
		cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("mock error")
		}

		// when
		deleted, err := manager.delete(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.False(t, deleted)
		assert.Equal(t, "failed to delete cluster resource 'for-johnsmith': mock error", err.Error())
		AssertThatNSTemplateSet(t, namespaceName, username, cl).
			HasFinalizer(). // finalizer was not added and nothing else was done
			HasConditions(UnableToTerminate("mock error"))
	})
}

func TestPromoteClusterResources(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(username, "advanced")

	t.Run("success", func(t *testing.T) {

		t.Run("upgrade from advanced to team tier by changing only the CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "team", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			codeNs := newNamespace("advanced", username, "code")
			crq := newClusterResourceQuota(username, "advanced")
			crb := newTektonClusterRoleBinding(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "team-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "team"),
					Containing(`"limits.cpu":"4","limits.memory":"15Gi"`)).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))

			t.Run("upgrade from advanced to team tier by changing only the CRB since CRQ is already changed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "team"),
						Containing(`"limits.cpu":"4","limits.memory":"15Gi"`)).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/tier", "team"))
			})
		})

		t.Run("promote from withemptycrq to advanced tier by removing the redundant CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources("abcde11"))
			codeNs := newNamespace("advanced", username, "code")
			crq := newClusterResourceQuota(username, "withemptycrq")
			crb := newTektonClusterRoleBinding(username, "withemptycrq")
			emptyCrq := newClusterResourceQuota(username, "withemptycrq")
			emptyCrq.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, emptyCrq, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq")).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq"))

			t.Run("promote from withemptycrq to advanced tier by changing only the CRQ since redundant CRQ is already removed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/templateref", "withemptycrq-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "withemptycrq"))

			})
		})

		t.Run("downgrade from advanced to basic tier by removing CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}). // no cluster resources in 'basic` tier
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("downgrade from advanced to basic tier by removing CRB since CRQ is already removed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}). // no cluster resources in 'basic` tier
					HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})

		t.Run("delete redundant cluster resources when ClusterResources field is nil in NSTemplateSet", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq") // no cluster resources, so the "advancedCRQ" should be deleted even if the tier contains the "advancedCRQ"
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}). // resources were deleted
				HasNoResource("tekton-view-for-"+username, &rbacv1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("upgrade from basic to advanced by creating only CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withClusterResources("abcde11"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Provisioning())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("upgrade from basic to advanced by creating CRB since CRQ is already created", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Provisioning())
				AssertThatCluster(t, cl).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
						Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
			})
		})

		t.Run("with another user", func(t *testing.T) {
			// given
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(username, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			anotherCrb := newTektonClusterRoleBinding("another", "basic")

			idlerDev := newIdler(username, username+"-dev")
			idlerCode := newIdler(username, username+"-code")
			idlerStage := newIdler(username, username+"-stage")
			anotherIdlerDev := newIdler("another", "another-dev")
			anotherIdlerCode := newIdler("another", "another-code")
			anotherIdlerStage := newIdler("another", "another-stage")

			t.Run("no redundant cluster resources to be deleted for the given user", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned()), withClusterResources("abcde11"))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb, idlerDev, idlerCode, idlerStage, anotherIdlerDev, anotherIdlerCode, anotherIdlerStage)

				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.False(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Provisioned())
				AssertThatCluster(t, cl).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(username+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(username+"-code", &toolchainv1alpha1.Idler{}).
					HasResource(username+"-stage", &toolchainv1alpha1.Idler{}).
					HasResource("another-dev", &toolchainv1alpha1.Idler{}).
					HasResource("another-code", &toolchainv1alpha1.Idler{}).
					HasResource("another-stage", &toolchainv1alpha1.Idler{})
			})

			t.Run("cluster resources should be deleted since it doesn't contain clusterResources template", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned()))
				manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCrb, crb)

				// when - let remove everything
				var err error
				updated := true
				for ; updated; updated, err = manager.ensure(logger, nsTmplSet) {
					require.NoError(t, err)
				}

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
					HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
					HasResource("another-tekton-view", &rbacv1.ClusterRoleBinding{})

			})
		})

		t.Run("delete only one redundant cluster resource during one call", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic") // no cluster resources, so the "advancedCRQ" should be deleted
			advancedCRQ := newClusterResourceQuota(username, "withemptycrq")
			anotherCRQ := newClusterResourceQuota(username, "withemptycrq")
			crb := newTektonClusterRoleBinding(username, "withemptycrq")
			anotherCRQ.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, advancedCRQ, anotherCRQ, crb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating()) //
			quotas := &quotav1.ClusterResourceQuotaList{}
			err = cl.List(context.TODO(), quotas, &client.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, quotas.Items, 1)
			AssertThatCluster(t, cl).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("it should delete the second for-empty CRQ since it's the last one", func(t *testing.T) {
				// when - should delete the second ClusterResourceQuota
				updated, err = manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				err = cl.List(context.TODO(), quotas, &client.ListOptions{})
				require.NoError(t, err)
				assert.Len(t, quotas.Items, 0)
				AssertThatCluster(t, cl).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

				t.Run("it should delete the CRB since both CRQs are already removed", func(t *testing.T) {
					// when - should delete the second ClusterResourceQuota
					updated, err = manager.ensure(logger, nsTmplSet)

					// then
					require.NoError(t, err)
					assert.True(t, updated)
					err = cl.List(context.TODO(), quotas, &client.ListOptions{})
					require.NoError(t, err)
					assert.Len(t, quotas.Items, 0)
					roleBindings := &rbacv1.ClusterRoleBindingList{}
					err = cl.List(context.TODO(), roleBindings, &client.ListOptions{})
					require.NoError(t, err)
					assert.Len(t, roleBindings.Items, 0)
				})
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("promotion to another tier fails because it cannot list current resources", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"), withConditions(Updating()))
			crq := newClusterResourceQuota(username, "fail")
			crb := newTektonClusterRoleBinding(username, "fail")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "fail-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "fail")).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "fail-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "fail"))
		})

		t.Run("fail to downgrade from advanced to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("some error")
			}

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"failed to delete an existing redundant cluster resource of name 'for-johnsmith' and gvk 'quota.openshift.io/v1, Kind=ClusterResourceQuota': some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
		})
	})
}

func TestUpdateClusterResources(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	crb := newTektonClusterRoleBinding(username, "advanced")
	crq := newClusterResourceQuota(username, "advanced")

	t.Run("success", func(t *testing.T) {

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"), withClusterResources("abcde12"))
			codeNs := newNamespace("advanced", username, "dev")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb, codeNs)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12")).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))

			t.Run("update from abcde11 revision to abcde12 revision by deleting CRB since CRQ is already changed", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12")).
					HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
			})
		})

		t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier by updating CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			crq := newClusterResourceQuota(username, "advanced", withTemplateRefUsingRevision("abcde12"))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier by creating CRB", func(t *testing.T) {
				// when
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).HasConditions(Updating())
				AssertThatCluster(t, cl).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("update to abcde11 fails because it cannot list current resources", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withClusterResources("abcde11"), withConditions(Updating()))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
			cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
		})

		t.Run("update to abcde13 fails because it find the template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withClusterResources("abcde13"), withConditions(Updating()))
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, crb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'advanced-clusterresources-abcde13' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"advanced-clusterresources-abcde13\" not found"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11")).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"))
		})
	})
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
