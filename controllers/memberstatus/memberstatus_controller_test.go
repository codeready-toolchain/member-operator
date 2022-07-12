package memberstatus

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	membercfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/status"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	routev1 "github.com/openshift/api/route/v1"
	"gopkg.in/h2non/gock.v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var requeueResult = reconcile.Result{RequeueAfter: 5 * time.Second}

const defaultMemberOperatorName = "member-operator"
const defaultMemberOperatorDeploymentName = "member-operator-controller-manager"

const defaultMemberStatusName = membercfg.MemberStatusName

// che test constants
const (
	testCheURL  = "http://codeready-codeready-workspaces-operator.member-cluster/che/"
	cheUserPath = "api/user"
)

func TestNoMemberStatusFound(t *testing.T) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	allNamespacesCl := test.NewFakeClient(t)

	t.Run("No memberstatus resource found", func(t *testing.T) {
		// given
		requestName := "bad-name"
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, _ := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then - there should not be any error, the controller should only log that the resource was not found
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("No memberstatus resource found - right name but not found", func(t *testing.T) {
		// given
		expectedErrMsg := "get failed"
		requestName := defaultMemberStatusName
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl)
		fakeClient.MockGet = func(ctx context.Context, key types.NamespacedName, obj client.Object) error {
			return fmt.Errorf(expectedErrMsg)
		}

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.Error(t, err)
		require.Equal(t, "unable to get MemberOperatorConfig: "+expectedErrMsg, err.Error())
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestOverallStatusCondition(t *testing.T) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	restore := test.SetEnvVarsAndRestore(t, test.Env(commonconfig.OperatorNameEnvVar, defaultMemberOperatorName))
	defer restore()
	nodeAndMetrics := newNodesAndNodeMetrics(
		forNode("worker-123", []string{"worker"}, "4000000Ki", withMemoryUsage("1250000Ki")),
		forNode("worker-345", []string{"worker"}, "6000000Ki", withMemoryUsage("2250000Ki")),
		forNode("worker-567", []string{"worker"}, "6000000Ki", withMemoryUsage("500000Ki")),
		forNode("master-123", []string{"master"}, "4000000Ki", withMemoryUsage("2000000Ki")),
		forNode("master-456", []string{"master"}, "5000000Ki", withMemoryUsage("1000000Ki")))

	allNamespacesCl := test.NewFakeClient(t, consoleRoute(), cheRoute(true))

	t.Run("All components ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsReady()).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())

		t.Run("when node has multiple roles", func(t *testing.T) {
			// given
			nodeAndMetrics := newNodesAndNodeMetrics(
				forNode("combined-123", []string{"master", "worker"}, "5000000Ki", withMemoryUsage("1000000Ki")))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("worker", 20), OfNodeRole("master", 20)).
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("ignore infra node", func(t *testing.T) {
			// given
			nodeAndMetrics := newNodesAndNodeMetrics(
				forNode("worker-123", []string{"worker"}, "4000000Ki", withMemoryUsage("3000000Ki")),
				forNode("infra-123", []string{"infra"}, "4000000Ki", withMemoryUsage("1250000Ki")),
				forNode("master-123", []string{"master"}, "6000000Ki", withMemoryUsage("3000000Ki")))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("worker", 75), OfNodeRole("master", 50)).
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("ignore infra node when node is shared", func(t *testing.T) {
			// given
			nodeAndMetrics := newNodesAndNodeMetrics(
				forNode("worker-123", []string{"worker"}, "4000000Ki", withMemoryUsage("3000000Ki")),
				forNode("infra-123", []string{"worker", "infra"}, "4000000Ki", withMemoryUsage("1250000Ki")),
				forNode("master-123", []string{"master"}, "6000000Ki", withMemoryUsage("3000000Ki")))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("worker", 75), OfNodeRole("master", 50)).
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})
	})

	t.Run("Host connection not found", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterNotExist
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnectionTag))).
			HasHostConditionErrorMsg("the cluster connection was not found").
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Host connection not ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterNotReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnectionTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Host connection probe not working", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterProbeNotWorking
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(hostConnectionTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Member operator deployment not found - deployment env var not set", func(t *testing.T) {
		// given
		resetFunc := test.UnsetEnvVarAndRestore(t, commonconfig.OperatorNameEnvVar)
		requestName := defaultMemberStatusName
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		resetFunc()
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperatorTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasMemberOperatorConditionErrorMsg("unable to get the deployment: OPERATOR_NAME must be set").
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Member operator deployment not found", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperatorTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Member operator deployment not ready", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentNotAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperatorTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("Member operator deployment not progressing", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentNotProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
			HasCondition(ComponentsNotReady(string(memberOperatorTag))).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("when missing only one NodeMetrics resource then it's fine", func(t *testing.T) {
		// given
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())

		// let's create another pair of Node and NodeMetrics resources - the resulting array will contain Node as the first object and NodeMetrics as the second object
		singleNodeAndMetrics := newNodesAndNodeMetrics(forNode("worker", []string{"worker"}, "3000000Ki"))
		// now use only the first object - Node - and don't add the NodeMetrics so we can simulate a situation when one NodeMetrics is missing
		reconciler, req, fakeClient := prepareReconcile(t, defaultMemberStatusName, newGetHostClusterReady, allNamespacesCl,
			append(nodeAndMetrics, singleNodeAndMetrics[0], memberOperatorDeployment, newMemberStatus())...)

		// when
		res, err := reconciler.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, requeueResult, res)
		AssertThatMemberStatus(t, req.Namespace, defaultMemberStatusName, fakeClient).
			HasCondition(ComponentsReady()).
			HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
			HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
	})

	t.Run("metrics failures", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady

		t.Run("when missing memory item", func(t *testing.T) {
			// given
			nodeAndMetrics := newNodesAndNodeMetrics(forNode("worker-123", []string{"worker"}, "3000000Ki"))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady(string("resourceUsage"))).
				HasMemoryUsage().
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("when unable to list Nodes", func(t *testing.T) {
			// given
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)
			fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.NodeList); ok {
					return fmt.Errorf("some error")
				}
				return fakeClient.Client.List(ctx, list, opts...)
			}

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady(string("resourceUsage"))).
				HasMemoryUsage().
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("when unable to list NodeMetrics", func(t *testing.T) {
			// given
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)
			fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*v1beta1.NodeMetricsList); ok {
					return fmt.Errorf("some error")
				}
				return fakeClient.Client.List(ctx, list, opts...)
			}

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady(string("resourceUsage"))).
				HasMemoryUsage().
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("when missing NodeMetrics for two Nodes", func(t *testing.T) {
			// given

			// let's the first pair of Node and NodeMetrics resources
			singleNodeAndMetrics1 := newNodesAndNodeMetrics(forNode("worker-a", []string{"worker"}, "3000000Ki"))
			// and lest' also create the second pair of Node and NodeMetrics resources
			singleNodeAndMetrics2 := newNodesAndNodeMetrics(forNode("worker-b", []string{"worker"}, "3000000Ki"))
			// since the arrays contain Node as the first object and NodeMetrics as the second object, we can now use only the first object from both of the arrays
			// and don't add the NodeMetrics so we can simulate a situation when the NodeMetrics resources are missing for both of the Nodes
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl,
				append(nodeAndMetrics, singleNodeAndMetrics1[0], singleNodeAndMetrics2[0], memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady(string("resourceUsage"))).
				HasMemoryUsage().
				HasRoutes("https://console.member-cluster/console/", "https://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})
	})

	t.Run("routes", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady

		t.Run("che not using tls with path", func(t *testing.T) {
			// given
			allNamespacesCl := test.NewFakeClient(t, consoleRoute(), cheRoute(false))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/console/", "http://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable())
		})

		t.Run("che and console without path", func(t *testing.T) {
			// given
			cheRoute := cheRoute(false)
			cheRoute.Spec.Path = ""
			consoleRoute := consoleRoute()
			consoleRoute.Spec.Path = ""
			allNamespacesCl := test.NewFakeClient(t, consoleRoute, cheRoute)
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/", "http://codeready-codeready-workspaces-operator.member-cluster/", routesAvailable())
		})

		t.Run("console route unavailable", func(t *testing.T) {
			// given
			allNamespacesCl := test.NewFakeClient(t, cheRoute(false))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady(string("routes"))).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("", "", consoleRouteUnavailable("routes.route.openshift.io \"console\" not found"))
		})

		t.Run("che route unavailable", func(t *testing.T) {
			// given
			allNamespacesCl := test.NewFakeClient(t, consoleRoute())

			t.Run("when not required", func(t *testing.T) {
				// given
				reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, memberOperatorDeployment, memberStatus)...)

				// when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				assert.Equal(t, requeueResult, res)
				AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
					HasCondition(ComponentsReady()).
					HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
					HasRoutes("https://console.member-cluster/console/", "", routesAvailable())
			})

			t.Run("when required", func(t *testing.T) {
				// given
				config := commonconfig.NewMemberOperatorConfigWithReset(t, testconfig.Che().Required(true))
				reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, config, memberOperatorDeployment, memberStatus)...)

				// when
				res, err := reconciler.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				assert.Equal(t, requeueResult, res)
				AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
					HasCondition(ComponentsNotReady(string("routes"))).
					HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
					HasRoutes("https://console.member-cluster/console/", "", cheRouteUnavailable("routes.route.openshift.io \"codeready\" not found"))
			})
		})
	})

	t.Run("che integration", func(t *testing.T) {
		// given
		requestName := defaultMemberStatusName
		memberOperatorDeployment := newMemberDeploymentWithConditions(status.DeploymentAvailableCondition(), status.DeploymentProgressingCondition())
		memberStatus := newMemberStatus()
		getHostClusterFunc := newGetHostClusterReady
		memberSecret := newMemberSecret("test-che-user", "test-che-password")

		t.Run("success", func(t *testing.T) {
			// given
			config := commonconfig.NewMemberOperatorConfigWithReset(t,
				testconfig.Che().
					UserDeletionEnabled(true).
					Secret().
					Ref("test-secret").
					CheAdminUsernameKey("che.admin.username").
					CheAdminPasswordKey("che.admin.password"))
			allNamespacesCl := test.NewFakeClient(t, consoleRoute(), cheRoute(false))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, config, memberOperatorDeployment, memberSecret, memberStatus)...)

			defer gock.OffAll()
			gock.New(testCheURL).
				Get(cheUserPath).
				MatchHeader("Authorization", "Bearer abc.123.xyz").
				Persist().
				Reply(200)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsReady()).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/console/", "http://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable()).
				HasCheConditions(cheReady())
		})

		t.Run("che admin user not configured (no member secret)", func(t *testing.T) {
			// given
			config := commonconfig.NewMemberOperatorConfigWithReset(t,
				testconfig.Che().
					Required(true).
					UserDeletionEnabled(true).
					Secret().
					Ref("test-secret").
					CheAdminUsernameKey("che.admin.username").
					CheAdminPasswordKey("che.admin.password"))
			allNamespacesCl := test.NewFakeClient(t, consoleRoute(), cheRoute(false))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, config, memberOperatorDeployment, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady("che")).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/console/", "http://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable()).
				HasCheConditions(cheAdminUserNotConfigured("the Che admin user credentials are not configured but Che user deletion is enabled"))
		})

		t.Run("no che route", func(t *testing.T) {
			// given
			config := commonconfig.NewMemberOperatorConfigWithReset(t,
				testconfig.Che().
					Required(true).
					UserDeletionEnabled(true).
					Secret().
					Ref("test-secret").
					CheAdminUsernameKey("che.admin.username").
					CheAdminPasswordKey("che.admin.password"))
			allNamespacesCl := test.NewFakeClient(t, consoleRoute())
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, config, memberOperatorDeployment, memberSecret, memberStatus)...)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady("routes", "che")).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/console/", "", cheRouteUnavailable(`routes.route.openshift.io "codeready" not found`)).
				HasCheConditions(cheRouteUnavailable(`Che dashboard URL unavailable but Che user deletion is enabled: routes.route.openshift.io "codeready" not found`))
		})

		t.Run("che API check failure", func(t *testing.T) {
			// given
			config := commonconfig.NewMemberOperatorConfigWithReset(t,
				testconfig.Che().
					Required(true).
					UserDeletionEnabled(true).
					Secret().
					Ref("test-secret").
					CheAdminUsernameKey("che.admin.username").
					CheAdminPasswordKey("che.admin.password"))
			allNamespacesCl := test.NewFakeClient(t, consoleRoute(), cheRoute(false))
			reconciler, req, fakeClient := prepareReconcile(t, requestName, getHostClusterFunc, allNamespacesCl, append(nodeAndMetrics, config, memberOperatorDeployment, memberSecret, memberStatus)...)
			defer gock.OffAll()
			gock.New(testCheURL).
				Get(cheUserPath).
				MatchHeader("Authorization", "Bearer abc.123.xyz").
				Persist().
				Reply(400).
				BodyString(`{"error":"che error"}`)

			// when
			res, err := reconciler.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, requeueResult, res)
			AssertThatMemberStatus(t, req.Namespace, requestName, fakeClient).
				HasCondition(ComponentsNotReady("che")).
				HasMemoryUsage(OfNodeRole("master", 33), OfNodeRole("worker", 25)).
				HasRoutes("https://console.member-cluster/console/", "http://codeready-codeready-workspaces-operator.member-cluster/che/", routesAvailable()).
				HasCheConditions(cheUserAPICheckError(`che user API check failed, Response status: '400 Bad Request' Body: '{"error":"che error"}'`))
		})
	})
}

func newMemberSecret(username, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: test.MemberOperatorNs,
		},
		Data: map[string][]byte{
			"che.admin.username": []byte(username),
			"che.admin.password": []byte(password),
		},
	}
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
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultMemberOperatorDeploymentName,
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

func prepareReconcile(t *testing.T, requestName string, getHostClusterFunc func(fakeClient client.Client) cluster.GetHostClusterFunc, allNamespacesClient *test.FakeClient, initObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	os.Setenv("WATCH_NAMESPACE", test.MemberOperatorNs)
	fakeClient := test.NewFakeClient(t, initObjs...)
	r := &Reconciler{
		Client:              fakeClient,
		AllNamespacesClient: allNamespacesClient,
		Scheme:              scheme.Scheme,
		GetHostCluster:      getHostClusterFunc(fakeClient),
		CheClient:           cheTestClient(allNamespacesClient),
	}
	return r, reconcile.Request{NamespacedName: test.NamespacedName(test.MemberOperatorNs, requestName)}, fakeClient
}

type nodeAndMetricsCreator func() (node *corev1.Node, nodeMetric *v1beta1.NodeMetrics)

func forNode(name string, roles []string, allocatableMemory string, metricsModifiers ...nodeMetricsModifier) nodeAndMetricsCreator {
	return func() (*corev1.Node, *v1beta1.NodeMetrics) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"beta.kubernetes.io/os":  "linux",
					"kubernetes.io/arch":     "amd64",
					"kubernetes.io/hostname": "ip-10-0-140-242",
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: map[corev1.ResourceName]resource.Quantity{
					"cpu":    resource.MustParse("3500m"),
					"memory": resource.MustParse(allocatableMemory),
					"pods":   resource.MustParse("250"),
				},
			},
		}
		for _, role := range roles {
			node.ObjectMeta.Labels["node-role.kubernetes.io/"+role] = ""
		}
		nodeMetrics := &v1beta1.NodeMetrics{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Usage: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU: resource.MustParse("3499m"),
			},
		}
		for _, modifyMetrics := range metricsModifiers {
			modifyMetrics(nodeMetrics)
		}
		return node, nodeMetrics
	}
}

type nodeMetricsModifier func(metrics *v1beta1.NodeMetrics)

func withMemoryUsage(usage string) nodeMetricsModifier {
	return func(metrics *v1beta1.NodeMetrics) {
		var resourceList map[corev1.ResourceName]resource.Quantity = metrics.Usage
		resourceList[corev1.ResourceMemory] = resource.MustParse(usage)
		metrics.Usage = resourceList
	}
}

func newNodesAndNodeMetrics(nodeAndMetricsCreators ...nodeAndMetricsCreator) []runtime.Object {
	var objects []runtime.Object
	for _, create := range nodeAndMetricsCreators {
		node, nodeMetrics := create()
		objects = append(objects, node, nodeMetrics)
	}
	return objects
}

func consoleRouteUnavailable(msg string) toolchainv1alpha1.Condition {
	return *status.NewComponentErrorCondition("ConsoleRouteUnavailable", msg)
}

func cheRouteUnavailable(msg string) toolchainv1alpha1.Condition {
	return *status.NewComponentErrorCondition("CheRouteUnavailable", msg)
}

func routesAvailable() toolchainv1alpha1.Condition {
	return *status.NewComponentReadyCondition("RoutesAvailable")
}

func consoleRoute() *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console",
			Namespace: "openshift-console",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("console.%s", test.MemberClusterName),
			Path: "console/",
		},
	}
}

func cheRoute(tls bool) *routev1.Route {
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codeready",
			Namespace: "codeready-workspaces-operator",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("codeready-codeready-workspaces-operator.%s", test.MemberClusterName),
			Path: "che/",
		},
	}
	if tls {
		r.Spec.TLS = &routev1.TLSConfig{
			Termination: "edge",
		}
	}
	return r
}

func cheTestClient(cl client.Client) *che.Client {
	tokenCache := che.NewTokenCacheWithToken(
		http.DefaultClient,
		&che.TokenSet{
			AccessToken:  "abc.123.xyz",
			Expiration:   time.Now().Add(99 * time.Hour).Unix(),
			ExpiresIn:    99,
			RefreshToken: "111.222.333",
			TokenType:    "bearer",
		})
	return che.NewCheClient(http.DefaultClient, cl, tokenCache)
}

func cheReady() toolchainv1alpha1.Condition {
	return *status.NewComponentReadyCondition("CheReady")
}

func cheAdminUserNotConfigured(msg string) toolchainv1alpha1.Condition {
	return *status.NewComponentErrorCondition("CheAdminUserNotConfigured", msg)
}

func cheUserAPICheckError(msg string) toolchainv1alpha1.Condition {
	return *status.NewComponentErrorCondition("CheUserAPICheckFailed", msg)
}

func TestSanitizeURL(t *testing.T) {
	t.Run("ends with single slash", func(t *testing.T) {
		// when
		sanitized := sanitizeURL("https://some/url/")

		// then
		assert.Equal(t, "https://some/url/", sanitized)
	})

	t.Run("ends with double slashes", func(t *testing.T) {
		// when
		sanitized := sanitizeURL("https://some/url//")

		// then
		assert.Equal(t, "https://some/url/", sanitized)
	})

	t.Run("ends without any slash", func(t *testing.T) {
		// when
		sanitized := sanitizeURL("https://some/url")

		// then
		assert.Equal(t, "https://some/url", sanitized)
	})
}
