package useraccountstatus

import (
	"context"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	commontest "github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" //nolint: staticcheck // not deprecated anymore: see https://github.com/kubernetes-sigs/controller-runtime/pull/1101
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestUpdateMasterUserRecordWithSingleEmbeddedUserAccount(t *testing.T) {
	// given
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	userAcc := newUserAccount("foo")
	mur := newMasterUserRecord("foo", "111111")

	t.Run("success", func(t *testing.T) {

		t.Run("should change the syncIndex", func(t *testing.T) {
			// given
			cntrl, hostClient := newReconcileStatus(t, userAcc, mur, true, v1.ConditionTrue)

			// when
			_, err := cntrl.Reconcile(newUaRequest(userAcc))

			// then
			require.NoError(t, err)
			currentMur := &toolchainv1alpha1.MasterUserRecord{}
			err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
			require.NoError(t, err)
			assert.Equal(t, "222222", currentMur.Spec.UserAccounts[0].SyncIndex)
		})

		t.Run("should reset the syncIndex", func(t *testing.T) {
			// given
			userAcc := newUserAccount("foo")
			now := metav1.Now()
			userAcc.DeletionTimestamp = &now
			cntrl, hostClient := newReconcileStatus(t, userAcc, mur, true, v1.ConditionTrue)

			// when
			_, err := cntrl.Reconcile(newUaRequest(userAcc))

			// then
			require.NoError(t, err)
			currentMur := &toolchainv1alpha1.MasterUserRecord{}
			err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
			require.NoError(t, err)
			assert.Equal(t, "0", currentMur.Spec.UserAccounts[0].SyncIndex)
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("host not available", func(t *testing.T) {
			// given
			cntrl, hostClient := newReconcileStatus(t, userAcc, mur, false, "")

			// when
			_, err := cntrl.Reconcile(newUaRequest(userAcc))

			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "there is no host cluster registered")
			currentMur := &toolchainv1alpha1.MasterUserRecord{}
			err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
			require.NoError(t, err)
			assert.Equal(t, "111111", currentMur.Spec.UserAccounts[0].SyncIndex)
		})

		t.Run("host not ready", func(t *testing.T) {
			// given
			cntrl, hostClient := newReconcileStatus(t, userAcc, mur, true, v1.ConditionFalse)

			// when
			_, err := cntrl.Reconcile(newUaRequest(userAcc))

			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the host cluster is not ready")
			currentMur := &toolchainv1alpha1.MasterUserRecord{}
			err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
			require.NoError(t, err)
			assert.Equal(t, "111111", currentMur.Spec.UserAccounts[0].SyncIndex)
		})
	})
}

func TestUpdateMasterUserRecordWithExistingEmbeddedUserAccount(t *testing.T) {
	// given
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	userAcc := newUserAccount("bar")
	mur := newMasterUserRecord("bar", "111111")
	mur.Spec.UserAccounts = append(mur.Spec.UserAccounts, toolchainv1alpha1.UserAccountEmbedded{
		TargetCluster: "second-member-cluster",
		SyncIndex:     "aaaaaa",
	})
	cntrl, hostClient := newReconcileStatus(t, userAcc, mur, true, v1.ConditionTrue)

	// when
	_, err := cntrl.Reconcile(newUaRequest(userAcc))

	// then
	require.NoError(t, err)
	currentMur := &toolchainv1alpha1.MasterUserRecord{}
	err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
	require.NoError(t, err)
	assert.Equal(t, "222222", currentMur.Spec.UserAccounts[0].SyncIndex)
	assert.Equal(t, commontest.MemberClusterName, currentMur.Spec.UserAccounts[0].TargetCluster)

	assert.Equal(t, "aaaaaa", currentMur.Spec.UserAccounts[1].SyncIndex)
	assert.Equal(t, "second-member-cluster", currentMur.Spec.UserAccounts[1].TargetCluster)
}

func TestUpdateMasterUserRecordWithoutUserAccountEmbedded(t *testing.T) {
	// given
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	userAcc := newUserAccount("johny")

	t.Run("when there is no UserAccount", func(t *testing.T) {
		mur := newMasterUserRecord("johny", "")
		mur.Spec.UserAccounts = []toolchainv1alpha1.UserAccountEmbedded{}
		cntrl, _ := newReconcileStatus(t, userAcc, mur, true, v1.ConditionTrue)

		// when
		_, err := cntrl.Reconcile(newUaRequest(userAcc))

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the MasterUserRecord 'johny' doesn't have any embedded UserAccount for cluster 'member-cluster'")
	})

	t.Run("when the cluster name is different", func(t *testing.T) {
		mur := newMasterUserRecord("johny", "")
		mur.Spec.UserAccounts[0].TargetCluster = "some-other-cluster"
		cntrl, _ := newReconcileStatus(t, userAcc, mur, true, v1.ConditionTrue)

		// when
		_, err := cntrl.Reconcile(newUaRequest(userAcc))

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the MasterUserRecord 'johny' doesn't have any embedded UserAccount for cluster 'member-cluster'")
	})
}

func newReconcileStatus(t *testing.T,
	userAcc *toolchainv1alpha1.UserAccount,
	mur *toolchainv1alpha1.MasterUserRecord,
	ok bool, status v1.ConditionStatus) (Reconciler, client.Client) {

	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	memberClient := fake.NewFakeClientWithScheme(s, userAcc)
	hostClient := fake.NewFakeClientWithScheme(s, mur)

	return Reconciler{
		Client:         memberClient,
		GetHostCluster: test.NewGetHostCluster(hostClient, ok, status),
		Scheme:         s,
		Log:            ctrl.Log.WithName("controllers").WithName("UserAccountStatus"),
	}, hostClient
}

func newUaRequest(userAcc *toolchainv1alpha1.UserAccount) reconcile.Request {
	return reconcile.Request{
		NamespacedName: namespacedName(userAcc.ObjectMeta),
	}
}

func namespacedName(obj metav1.ObjectMeta) types.NamespacedName {
	return types.NamespacedName{
		Namespace: obj.Namespace,
		Name:      obj.Name,
	}
}

func newUserAccount(userName string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            userName,
			ResourceVersion: "222222",
		},
	}
	return userAcc
}

func newMasterUserRecord(userName, syncIndex string) *toolchainv1alpha1.MasterUserRecord {
	userAcc := &toolchainv1alpha1.MasterUserRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: commontest.HostOperatorNs,
		},
		Spec: toolchainv1alpha1.MasterUserRecordSpec{
			UserAccounts: []toolchainv1alpha1.UserAccountEmbedded{{
				TargetCluster: commontest.MemberClusterName,
				SyncIndex:     syncIndex,
			}},
		},
	}
	return userAcc
}
