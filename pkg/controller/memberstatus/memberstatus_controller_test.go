package memberstatus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/api/pkg/apis"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/status"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var requeueResult = reconcile.Result{RequeueAfter: 5 * time.Second}

const defaultMemberOperatorName = "member-operator"

const defaultMemberStatusName = configuration.DefaultMemberStatusName

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

	t.Run("No memberstatus resource found - right name but not found", func(t *testing.T) {
		// given
		expectedErrMsg := "get failed"
		requestName := defaultMemberStatusName
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc)
		fakeClient.MockGet = func(ctx context.Context, key types.NamespacedName, obj runtime.Object) error {
			return fmt.Errorf(expectedErrMsg)
		}

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.Error(t, err)
		require.Equal(t, expectedErrMsg, err.Error())
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestOverallStatusCondition(t *testing.T) {
	restore := test.SetEnvVarsAndRestore(t, test.Env(k8sutil.OperatorNameEnvVar, defaultMemberOperatorName))
	defer restore()
	t.Run("All components ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
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
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
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
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).HasHostConditionErrorMsg("the cluster connection was not found")
	})

	t.Run("Host connection not ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
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

	t.Run("Host connection probe not working", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterProbeNotWorking
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberOperatorDeployment, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnection)))
	})

	t.Run("Member operator deployment not found - deployment env var not set", func(t *testing.T) {
		// given
		resetFunc := test.UnsetEnvVarAndRestore(t, k8sutil.OperatorNameEnvVar)
		requestName := defaultMemberStatusName
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, memberStatus)

		// when
		res, err := reconciler.Reconcile(req)

		// then
		resetFunc()
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperator)))
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).HasMemberOperatorConditionErrorMsg("unable to get the deployment: OPERATOR_NAME must be set")
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
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentNotAvailableCondition(), status.DeploymentProgressingCondition())
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
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentNotProgressingCondition())
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

func newMemberDeploymentWithConditions(deploymentConditions ...appsv1.DeploymentCondition) *appsv1.Deployment {
	memberOperatorDeploymentName := defaultMemberOperatorName
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
	return NewGetHostClusterWithProbe(fakeClient, true, corev1.ConditionTrue, metav1.Now())
}

func newGetHostClusterNotReady(fakeClient client.Client) cluster.GetHostClusterFunc {
	return NewGetHostClusterWithProbe(fakeClient, true, corev1.ConditionFalse, metav1.Now())
}

func newGetHostClusterProbeNotWorking(fakeClient client.Client) cluster.GetHostClusterFunc {
	aMinuteAgo := metav1.Time{
		Time: time.Now().Add(time.Duration(-60 * time.Second)),
	}
	return NewGetHostClusterWithProbe(fakeClient, true, corev1.ConditionTrue, aMinuteAgo)
}

func newGetHostClusterNotExist(fakeClient client.Client) cluster.GetHostClusterFunc {
	return NewGetHostClusterWithProbe(fakeClient, false, corev1.ConditionFalse, metav1.Now())
}

func prepareReconcile(t *testing.T, requestName string, getHostClusterFunc func(fakeClient client.Client) cluster.GetHostClusterFunc, initObjs ...runtime.Object) (*ReconcileMemberStatus, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)
	config, err := configuration.LoadConfig(fakeClient)
	require.NoError(t, err)
	r := &ReconcileMemberStatus{
		client:         fakeClient,
		scheme:         s,
		getHostCluster: getHostClusterFunc(fakeClient),
		config:         config,
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, requestName)}, fakeClient
}
