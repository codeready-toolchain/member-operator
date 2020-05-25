package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/codeready-toolchain/member-operator/test"
	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

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
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
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

	t.Run("should create only one CRQ when the template contains two of them", func(t *testing.T) {
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
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
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
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
			AssertThatCluster(t, fakeClient).
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
			HasConditions(UnableToProvisionClusterResources("unable to list cluster resources"))
	})

	t.Run("fail to get template containing cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "fail", withNamespaces("dev"), withClusterResources())
		manager, fakeClient := prepareClusterResourcesManager(t, nsTmplSet)

		// when
		_, err := manager.ensure(log, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve TierTemplate for the cluster resources with the name 'fail-clusterresources-12345bb'")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionClusterResources(
				"unable to retrieve the TierTemplate 'fail-clusterresources-12345bb' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"fail-clusterresources-12345bb\" not found"))
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
				"unable to create resource of kind: ClusterResourceQuota, version: v1: unable to create resource of kind: ClusterResourceQuota, version: v1: some error"))
	})
}

func TestDeleteClusterResources(t *testing.T) {

	username := "johnsmith"
	namespaceName := "toolchain-member"
	crq := newClusterResourceQuota(username, "advanced")
	nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withDeletionTs(), withClusterResources())

	t.Run("delete ClusterResourceQuota", func(t *testing.T) {
		// given
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)

		// when
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
	})

	t.Run("should delete only one ClusterResourceQuota even when tier contains more of them", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "withemptycrq", withNamespaces("dev"), withClusterResources())
		crq := newClusterResourceQuota(username, "withemptycrq")
		emptyCrq := newClusterResourceQuota("empty", "withemptycrq")
		manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq, emptyCrq)

		// when
		deleted, err := manager.delete(log, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, deleted)
		AssertThatCluster(t, cl).
			HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
		AssertThatCluster(t, cl).
			HasResource("for-empty", &quotav1.ClusterResourceQuota{})

		t.Run("delete the for-empty CRQ since it's the last one to be deleted", func(t *testing.T) {
			// when
			deleted, err := manager.delete(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, deleted)
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
			AssertThatCluster(t, cl).
				HasNoResource("for-empty", &quotav1.ClusterResourceQuota{})
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
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}, HasDeletionTimestamp())
		AssertThatCluster(t, cl).
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
		assert.Equal(t, "failed to delete cluster resource 'for-johnsmith': mock error", err.Error())
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

	t.Run("success", func(t *testing.T) {

		t.Run("upgrade from advanced to team tier by changing the CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "team", withNamespaces("dev"), withClusterResources())
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
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "team-clusterresources-12345bb"),
					WithLabel("toolchain.dev.openshift.com/tier", "team"),
					Containing(`"limits.cpu":"4","limits.memory":"15Gi"`))
		})

		t.Run("downgrade from advanced to basic tier by removing CRQ", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
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
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // no cluster resource quota in 'basic` tier
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
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // resource was deleted
		})

		t.Run("upgrade from basic to advanced by creating CRQ", func(t *testing.T) {
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
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-12345bb"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"),
					Containing(`"limits.cpu":"2","limits.memory":"10Gi"`)) // upgraded
		})

		t.Run("no redundant cluster resource quota to be deleted for the given user", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned()), withClusterResources())
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(username, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Provisioned())
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
		})

		t.Run("cluster resource quota should be deleted for the given user", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withConditions(Provisioned()))
			anotherNsTmplSet := newNSTmplSet(namespaceName, "another-user", "basic")
			advancedCRQ := newClusterResourceQuota(username, "advanced")
			anotherCRQ := newClusterResourceQuota("another-user", "basic")
			manager, cl := prepareClusterResourcesManager(t, anotherNsTmplSet, anotherCRQ, nsTmplSet, advancedCRQ)

			// when
			updated, err := manager.ensure(log, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, cl).
				HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
		})

		t.Run("delete only one redundant cluster resource during one call", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a cluster resource
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic") // no cluster resources, so the "advancedCRQ" should be deleted
			advancedCRQ := newClusterResourceQuota(username, "withemptycrq")
			anotherCRQ := newClusterResourceQuota(username, "withemptycrq")
			anotherCRQ.Name = "for-empty"
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, advancedCRQ, anotherCRQ)

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

			t.Run("it should delete the second for-empty CRQ since it's the last one", func(t *testing.T) {
				// when - should delete the second ClusterResourceQuota
				updated, err = manager.ensure(log, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				err = cl.List(context.TODO(), quotas, &client.ListOptions{})
				require.NoError(t, err)
				assert.Len(t, quotas.Items, 0)
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
			assert.False(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'fail-clusterresources-12345bb' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"fail-clusterresources-12345bb\" not found"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "fail-clusterresources-12345bb"),
					WithLabel("toolchain.dev.openshift.com/tier", "fail"))
		})

		t.Run("fail to downgrade from advanced to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			crq := newClusterResourceQuota(username, "advanced")
			manager, cl := prepareClusterResourcesManager(t, nsTmplSet, crq)
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
				HasConditions(UpdateFailed("failed to delete object 'for-johnsmith' of kind 'ClusterResourceQuota' in namespace '': some error"))
			AssertThatCluster(t, cl).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-12345bb"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
		})
	})
}
