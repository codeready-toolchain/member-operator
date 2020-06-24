package memberstatus

import (
	"testing"

	"github.com/codeready-toolchain/api/pkg/apis"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var requeueResult = reconcile.Result{RequeueAfter: defaultRequeueTime}

const defaultMemberOperatorName = "member-operator"

func TestNoMemberStatusFound(t *testing.T) {
	t.Run("No memberstatus resource found", func(t *testing.T) {
		// given
		requestName := "bad-name"
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, _ := prepareReconcile(t, requestName, getHostClusterFunc)

		// when
		res, err := reconciler.Reconcile(req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestOverallStatusCondition(t *testing.T) {
	restore := test.SetEnvVarsAndRestore(t, test.Env(OperatorNameVar, defaultMemberOperatorName))
	defer restore()
	t.Run("All components ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(t, MemberDeploymentReadyCondition(), MemberDeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsReady())
	})

	t.Run("Host connection not found", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(t, MemberDeploymentReadyCondition(), MemberDeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterNotExist
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnection)))
	})

	t.Run("Host connection not ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(t, MemberDeploymentReadyCondition(), MemberDeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterNotReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnection)))
	})

	t.Run("Member operator deployment not found", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperator)))
	})

	t.Run("Member operator deployment not ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(t, MemberDeploymentNotReadyCondition(), MemberDeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperator)))
	})

	t.Run("Member operator deployment not progressing", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(t, MemberDeploymentReadyCondition(), MemberDeploymentNotProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperator)))
	})
}

func newMemberStatus() *toolchainv1alpha1.MemberStatus {
	return &toolchainv1alpha1.MemberStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultMemberStatusName,
			Namespace: test.MemberOperatorNs,
		},
	}
}

func newMemberDeploymentWithConditions(t *testing.T, deploymentConditions ...appsv1.DeploymentCondition) *appsv1.Deployment {
	memberOperatorDeploymentName, err := getMemberOperatorDeploymentName()
	require.NoError(t, err)
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      memberOperatorDeploymentName,
			Namespace: test.MemberOperatorNs,
			Labels: map[string]string{
				"foo": "bar",
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			Conditions: deploymentConditions,
		},
	}
}

func newGetHostClusterReady(fakeClient client.Client) cluster.GetHostClusterFunc {
	return NewGetHostCluster(fakeClient, true, corev1.ConditionTrue)
}

func newGetHostClusterNotReady(fakeClient client.Client) cluster.GetHostClusterFunc {
	return NewGetHostCluster(fakeClient, true, corev1.ConditionFalse)
}

func newGetHostClusterNotExist(fakeClient client.Client) cluster.GetHostClusterFunc {
	return NewGetHostCluster(fakeClient, false, corev1.ConditionFalse)
}

func prepareReconcile(t *testing.T, requestName string, getHostClusterFunc func(fakeClient client.Client) cluster.GetHostClusterFunc, initObjs ...runtime.Object) (*ReconcileMemberStatus, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)

	r := &ReconcileMemberStatus{
		client:         fakeClient,
		scheme:         s,
		getHostCluster: getHostClusterFunc(fakeClient),
	}
	return r, reconcile.Request{test.NamespacedName(test.MemberOperatorNs, requestName)}, fakeClient
}

func MemberDeploymentReadyCondition() appsv1.DeploymentCondition {
	return appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionTrue,
	}
}

func MemberDeploymentNotReadyCondition() appsv1.DeploymentCondition {
	return appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentAvailable,
		Status: corev1.ConditionFalse,
	}
}

func MemberDeploymentProgressingCondition() appsv1.DeploymentCondition {
	return appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionTrue,
	}
}

func MemberDeploymentNotProgressingCondition() appsv1.DeploymentCondition {
	return appsv1.DeploymentCondition{
		Type:   appsv1.DeploymentProgressing,
		Status: corev1.ConditionFalse,
	}
}
