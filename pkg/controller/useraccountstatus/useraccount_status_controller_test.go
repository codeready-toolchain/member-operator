package useraccountstatus

import (
	"context"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/kubefed/pkg/apis/core/common"
	"sigs.k8s.io/kubefed/pkg/apis/core/v1beta1"
	"testing"
)

const (
	memberClusterName = "member-cluster"
	hostOperatorNs    = "toolchain-host-operator"
)

type getHostCluster func(cl client.Client) func() (*cluster.FedCluster, bool)

func TestUpdateMasterUserRecordWithSingleEmbeddedUserAccount(t *testing.T) {
	// given
	logf.SetLogger(logf.ZapLogger(true))
	userAcc := newUserAccount("johny", "222222")
	mur := newMasterUserRecord("johny", "111111")

	t.Run("successful - should change the syncIndex", func(t *testing.T) {

		cntrl, hostClient := newReconcileStatus(t, userAcc, mur, newGetHostCluster(true, v1.ConditionTrue))

		// when
		_, err := cntrl.Reconcile(newUaRequest(userAcc))

		// then
		require.NoError(t, err)
		currentMur := &toolchainv1alpha1.MasterUserRecord{}
		err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
		require.NoError(t, err)
		assert.Equal(t, "222222", currentMur.Spec.UserAccounts[0].SyncIndex)
	})

	t.Run("failed - host not available", func(t *testing.T) {

		cntrl, hostClient := newReconcileStatus(t, userAcc, mur, newGetHostCluster(false, ""))

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

	t.Run("failed - host not ready", func(t *testing.T) {

		cntrl, hostClient := newReconcileStatus(t, userAcc, mur, newGetHostCluster(true, v1.ConditionFalse))

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
}

func TestUpdateMasterUserRecordWithExistingEmbeddedUserAccount(t *testing.T) {
	// given
	logf.SetLogger(logf.ZapLogger(true))
	userAcc := newUserAccount("johny", "222222")
	mur := newMasterUserRecord("johny", "111111")
	mur.Spec.UserAccounts = append(mur.Spec.UserAccounts, toolchainv1alpha1.UserAccountEmbedded{
		TargetCluster: "second-member-cluster",
		SyncIndex:     "aaaaaa",
	})
	cntrl, hostClient := newReconcileStatus(t, userAcc, mur, newGetHostCluster(true, v1.ConditionTrue))

	// when
	_, err := cntrl.Reconcile(newUaRequest(userAcc))

	// then
	require.NoError(t, err)
	currentMur := &toolchainv1alpha1.MasterUserRecord{}
	err = hostClient.Get(context.TODO(), namespacedName(mur.ObjectMeta), currentMur)
	require.NoError(t, err)
	assert.Equal(t, "222222", currentMur.Spec.UserAccounts[0].SyncIndex)
	assert.Equal(t, memberClusterName, currentMur.Spec.UserAccounts[0].TargetCluster)

	assert.Equal(t, "aaaaaa", currentMur.Spec.UserAccounts[1].SyncIndex)
	assert.Equal(t, "second-member-cluster", currentMur.Spec.UserAccounts[1].TargetCluster)
}

func TestUpdateMasterUserRecordWithoutUserAccountEmbedded(t *testing.T) {
	// given
	logf.SetLogger(logf.ZapLogger(true))
	userAcc := newUserAccount("johny", "222222")

	t.Run("when there is no UserAccount", func(t *testing.T) {
		mur := newMasterUserRecord("johny", "")
		mur.Spec.UserAccounts = []toolchainv1alpha1.UserAccountEmbedded{}
		cntrl, _ := newReconcileStatus(t, userAcc, mur, newGetHostCluster(true, v1.ConditionTrue))

		// when
		_, err := cntrl.Reconcile(newUaRequest(userAcc))

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the MasterUserRecord doesn't have UserAccount embedded")
	})

	t.Run("when the cluster name is different", func(t *testing.T) {
		mur := newMasterUserRecord("johny", "")
		mur.Spec.UserAccounts[0].TargetCluster = "some-other-cluster"
		cntrl, _ := newReconcileStatus(t, userAcc, mur, newGetHostCluster(true, v1.ConditionTrue))

		// when
		_, err := cntrl.Reconcile(newUaRequest(userAcc))

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the MasterUserRecord doesn't have UserAccount embedded")
	})
}

func newReconcileStatus(t *testing.T,
	userAcc *toolchainv1alpha1.UserAccount,
	mur *toolchainv1alpha1.MasterUserRecord,
	getHostCluster getHostCluster) (ReconcileUserAccountStatus, client.Client) {

	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	memberClient := fake.NewFakeClientWithScheme(s, userAcc)
	hostClient := fake.NewFakeClientWithScheme(s, mur)

	return ReconcileUserAccountStatus{
		client:         memberClient,
		getHostCluster: getHostCluster(hostClient),
		scheme:         s,
	}, hostClient
}

func newGetHostCluster(ok bool, status v1.ConditionStatus) getHostCluster {
	if !ok {
		return func(cl client.Client) func() (*cluster.FedCluster, bool) {
			return func() (fedCluster *cluster.FedCluster, b bool) {
				return nil, false
			}
		}
	}
	return func(cl client.Client) func() (*cluster.FedCluster, bool) {
		return func() (fedCluster *cluster.FedCluster, b bool) {
			return &cluster.FedCluster{
				Client:            cl,
				Type:              cluster.Host,
				OperatorNamespace: hostOperatorNs,
				LocalName:         memberClusterName,
				ClusterStatus: &v1beta1.KubeFedClusterStatus{
					Conditions: []v1beta1.ClusterCondition{{
						Type:   common.ClusterReady,
						Status: status,
					}},
				},
			}, true
		}
	}
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

func newUserAccount(userName, resourceVersion string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            userName,
			ResourceVersion: resourceVersion,
		},
	}
	return userAcc
}

func newMasterUserRecord(userName, syncIndex string) *toolchainv1alpha1.MasterUserRecord {
	userAcc := &toolchainv1alpha1.MasterUserRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: hostOperatorNs,
		},
		Spec: toolchainv1alpha1.MasterUserRecordSpec{
			UserAccounts: []toolchainv1alpha1.UserAccountEmbedded{{
				TargetCluster: memberClusterName,
				SyncIndex:     syncIndex,
			}},
		},
	}
	return userAcc
}
