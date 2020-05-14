package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/api/rbac/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestWatchedClusterResources(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	for _, watchedClusterRes := range watchedClusterResources {
		johnyObject := watchedClusterRes.object.DeepCopyObject()
		objAccessor, err := meta.Accessor(johnyObject)
		require.NoError(t, err)
		objAccessor.SetLabels(map[string]string{"toolchain.dev.openshift.com/owner": "johny"})
		objAccessor.SetName("johny-object")

		anotherObject := watchedClusterRes.object.DeepCopyObject()
		anotherObjAccessor, err := meta.Accessor(anotherObject)
		require.NoError(t, err)
		anotherObjAccessor.SetLabels(map[string]string{"toolchain.dev.openshift.com/owner": "another"})
		anotherObjAccessor.SetName("another-object")
		namespace := newNamespace("basic", "johny", "code")

		t.Run("listExistingResources should return one resource of gvk "+watchedClusterRes.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject, namespace)

			// when
			existingResources, err := watchedClusterRes.listExistingResources(fakeClient, "johny")

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 1)
			assert.Equal(t, johnyObject, existingResources[0])
		})

		t.Run("listExistingResources should return not return any resource of gvk "+watchedClusterRes.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, namespace)

			// when
			existingResources, err := watchedClusterRes.listExistingResources(fakeClient, "johny")

			// then
			require.NoError(t, err)
			require.Len(t, existingResources, 0)
		})

		t.Run("listExistingResources should return an error when listing resources of gvk "+watchedClusterRes.gvk.String(), func(t *testing.T) {
			// given
			fakeClient := test.NewFakeClient(t, anotherObject, johnyObject)
			fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
				return fmt.Errorf("some error")
			}

			// when
			existingResources, err := watchedClusterRes.listExistingResources(fakeClient, "johny")

			// then
			require.Error(t, err)
			require.Len(t, existingResources, 0)
		})
	}

	t.Run("verify ClusteResourceQuota is in watchedClusterResources", func(t *testing.T) {
		// given
		clusterResource := watchedClusterResources[0]

		// then
		assert.Equal(t, &quotav1.ClusterResourceQuota{}, clusterResource.object)
		assert.Equal(t, quotav1.GroupVersion.WithKind("ClusterResourceQuota"), clusterResource.gvk)
	})
}

func TestEnsureClusterResourcesOK(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())

	t.Run("should create CRQ and set status to provisioning", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
			HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
	})

	t.Run("should not create ClusterResource objects when the field is nil", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"))
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasNoConditions()
	})

	t.Run("should create only one CRQ and not watched cluster resources when the template contains two CRQs", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("dev"), withClusterResources())
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioning())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
			HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})

		AssertThatCluster(t, fakeClient).
			HasNoResource("for-empty", &quotav1.ClusterResourceQuota{})

		t.Run("should create the second CRQ when the first one is already created", func(t *testing.T) {
			// when
			createdOrUpdated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, createdOrUpdated)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasConditions(Provisioning())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{}).
				HasResource("for-empty", &quotav1.ClusterResourceQuota{})
		})
	})

	t.Run("should not do anything when the CRQ is already created", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources(), withConditions(Provisioned()))
		crq := newClusterResourceQuota(username, "advanced")
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet, crq)

		// when
		createdOrUpdated, err := manager.ensure(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{})

		// since the resources weren't created as part of the test setup, they should not exist:
		AssertThatCluster(t, fakeClient).
			HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
			HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
	})
}

func TestEnsureClusterResourcesFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())

	t.Run("fail to list cluster resources", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return errors.New("unable to list cluster resources")
		}

		// when
		_, err := manager.ensure(log, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to list cluster resources")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"failed to list existing cluster resources of GVK 'quota.openshift.io/v1, Kind=ClusterResourceQuota': unable to list cluster resources"))
	})

	t.Run("fail to get template containing cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "fail", withNamespaces("dev"), withClusterResources())
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		_, err := manager.ensure(log, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve template")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"failed to retrieve template for the cluster resources of GVK 'quota.openshift.io/v1, Kind=ClusterResourceQuota': failed to retrieve template"))
	})

	t.Run("fail to create cluster resources", func(t *testing.T) {
		// given
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("some error")
		}

		// when
		_, err := manager.ensure(log, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "some error")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"unable to create resource of kind: ClusterRoleBinding, version: v1alpha1: unable to create resource of kind: ClusterRoleBinding, version: v1alpha1: some error"))
	})
}

func TestDeleteClusterResources(t *testing.T) {

	username := "johnsmith"
	namespaceName := "toolchain-member"
	crq := newClusterResourceQuota(username, "advanced")
	cr := newTektonClusterRole(username, "12345bb", "advanced", "ClusterTask")
	crb := newTektonClusterRoleBinding(username, "12345bb", "advanced")
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withDeletionTs(), withClusterResources())

	t.Run("delete ClusterResourceQuota", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, cr, crb)

		// when
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
			HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
	})

	t.Run("should delete not-watched-cluster resources and only one ClusterResourceQuota even when tier contains more ", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("dev"), withClusterResources())
		crq := newClusterResourceQuota(username, "withemptycrq")
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq, cr, crb)

		// when
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasResource("for-empty", &quotav1.ClusterResourceQuota{}).
			HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
			HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})

		t.Run("delete the for-empty CRQ since it's the last one to be deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{}).
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})
	})

	t.Run("delete the second ClusterResourceQuota since the first one has deletion timestamp set", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("dev"), withClusterResources())
		crq := newClusterResourceQuota(username, "withemptycrq")
		deletionTS := v1.NewTime(time.Now())
		crq.SetDeletionTimestamp(&deletionTS)
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq)

		// when
		deleted, err := manager.delete(log, nsTmplSet)

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
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
	})

	t.Run("failed to delete CRQ", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
		cl.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("mock error")
		}

		// when
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.Error(t, err)
		assert.False(t, deleted)
		assert.Equal(t, "failed to delete cluster resource 'johnsmith-tekton-view': mock error", err.Error())
		AssertThatNSTemplateSet(t, namespaceName, username, cl).
			HasFinalizer(). // finalizer was not added and nothing else was done
			HasConditions(UnableToTerminate("mock error"))
	})
}

func TestPromoteClusterResources(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	cr := newTektonClusterRole(username, "12345bb", "advanced", "ClusterTask")
	crb := newTektonClusterRoleBinding(username, "12345bb", "advanced")

	t.Run("success", func(t *testing.T) {

		t.Run("upgrade from advanced to team tier by changing the CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "team", withNamespaces("dev"), withClusterResources())
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, cr, crb)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

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
				HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{},
					WithLabel("toolchain.dev.openshift.com/tier", "team"),
					Containing(`"resources":["*"]`)).
				HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/tier", "team"))
		})

		t.Run("downgrade from advanced to basic tier by removing CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, cr, crb)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}). // no cluster resources in 'basic` tier
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})

		t.Run("delete redundant cluster resources when ClusterResources field is nil in NSTemplateSet", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq") // no cluster resources, so the "advancedCRQ" should be deleted even if the tier contains the "advancedCRQ"
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}). // resources were deleted
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})

		t.Run("upgrade from basic to advanced by creating CRQ and not-watched-cluster resources", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withClusterResources())
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

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
				HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"resources":["ClusterTask"]`)).
				HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
		})

		t.Run("no redundant cluster resources to be deleted for the given user", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned()), withClusterResources())
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(username, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			anotherCr := newTektonClusterRole("another", "12345bb", "basic", "ClusterTask")
			anotherCrb := newTektonClusterRoleBinding("another", "12345bb", "basic")
			manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, anotherCr, anotherCrb, cr, crb)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Provisioned())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasResource("for-another-user", &quotav1.ClusterResourceQuota{}).
				HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{}).
				HasResource("tekton-view-for-another", &v1alpha1.ClusterRole{}).
				HasResource("another-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})

		t.Run("cluster resources should be deleted since it doesn't contain clusterResources template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned())) // no cluster resources template, so the cluster resources should be deleted
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(username, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ, cr, crb)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("another-user", &quotav1.ClusterResourceQuota{}).
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})

		t.Run("delete not-watched-cluster resources and only one redundant cluster resource during one call", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic") // no cluster resources, so the "advancedCRQ" should be deleted
			advancedCRQ := newClusterResourceQuota(username, "withemptycrq")
			anotherCRQ := newClusterResourceQuota(username, "withemptycrq")
			cr := newTektonClusterRole(username, "12345bb", "withemptycrq", "ClusterTask")
			crb := newTektonClusterRoleBinding(username, "12345bb", "withemptycrq")
			anotherCRQ.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, advancedCRQ, anotherCRQ, cr, crb)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

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
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})

			t.Run("it should delete the second for-empty CRQ since it's the last one", func(t *testing.T) {
				// when - should delete the second ClusterResourceQuota
				updated, err = manager.ensure(log, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				err = cl.List(context.TODO(), quotas, &client.ListOptions{})
				require.NoError(t, err)
				assert.Len(t, quotas.Items, 0)
				AssertThatCluster(t, cl).
					HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
					HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("promotion to another tier fails because it cannot load current template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			crq := newClusterResourceQuota(username, "fail")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.Error(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"failed to get current cluster resources from template of a tier fail: failed to retrieve template"))
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasNoResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{}).
				HasNoResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{})
		})

		t.Run("fail to downgrade from advanced to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, cr, crb)
			cl.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("some error")
			}

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.Error(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"failed to delete an existing redundant cluster resource of name 'for-johnsmith' and gvk 'quota.openshift.io/v1, Kind=ClusterResourceQuota': some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
				HasResource("tekton-view-for-"+username, &v1alpha1.ClusterRole{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
				HasResource(username+"-tekton-view", &v1alpha1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
		})
	})
}

func TestRetainFunctions(t *testing.T) {
	// given
	clusterRole := runtime.RawExtension{Object: &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "ClusterRole",
			"apiVersion": "rbac.authorization.k8s.io/v1alpha1",
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
			"apiVersion": "rbac.authorization.k8s.io/v1alpha1",
		}}}

	t.Run("verify retainObjectsOfSameGVK function for ClusterRole", func(t *testing.T) {
		// given
		retain := retainObjectsOfSameGVK(v1alpha1.SchemeGroupVersion.WithKind("ClusterRole"))

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

	t.Run("verify retainAllObjectsButWatchedClusterResources function", func(t *testing.T) {

		t.Run("should return true since the resources are not one of the watched-cluster-resources", func(t *testing.T) {
			for _, obj := range []runtime.RawExtension{namespace, clusterRole, clusterRoleBinding} {

				// when
				ok := retainAllObjectsButWatchedClusterResources(obj)

				// then
				assert.True(t, ok)

			}
		})

		t.Run("should return false since the resource is one of the watched-cluster-resources", func(t *testing.T) {

			// when
			ok := retainAllObjectsButWatchedClusterResources(clusterResQuota)

			// then
			assert.False(t, ok)
		})
	})

	t.Run("verify retainAllObjectsButWatchedClusterResources function", func(t *testing.T) {

		t.Run("should return false since the resources are not one of the watched-cluster-resources", func(t *testing.T) {
			for _, obj := range []runtime.RawExtension{namespace, clusterRole, clusterRoleBinding} {

				// when
				ok := retainAllWatchedClusterResourceObjects(obj)

				// then
				assert.False(t, ok)

			}
		})

		t.Run("should return true since the resource is one of the watched-cluster-resources", func(t *testing.T) {

			// when
			ok := retainAllWatchedClusterResourceObjects(clusterResQuota)

			// then
			assert.True(t, ok)
		})
	})
}
