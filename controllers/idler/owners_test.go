package idler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/owners"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"github.com/stretchr/testify/require"
	"gopkg.in/h2non/gock.v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	fakescale "k8s.io/client-go/scale/fake"
	clienttest "k8s.io/client-go/testing"
)

type payloadTestConfig struct {
	podOwnerName    string
	expectedAppName string
	ownerScaledUp   func(*test.IdleablePayloadAssertion)
	ownerScaledDown func(*test.IdleablePayloadAssertion)
}

type createTestConfigFunc func(payloads) payloadTestConfig

var testConfigs = map[string]createTestConfigFunc{
	"Deployment": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with a nested controllers (Deployment -> ReplicaSet -> Pod) here,
			// so we the pod's owner is ReplicaSet but the expected scaled app is the parent Deployment.
			podOwnerName:    fmt.Sprintf("%s-replicaset", plds.deployment.Name),
			expectedAppName: plds.deployment.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DeploymentScaledUp(plds.deployment)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DeploymentScaledDown(plds.deployment)
			},
		}
	},
	"Integration": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with nested controllers (Integration -> Deployment -> ReplicaSet -> Pod) here,
			// so the pod's owner is ReplicaSet but the expected scaled app is the top-parent Integration CR.
			podOwnerName:    fmt.Sprintf("%s-deployment-replicaset", plds.integration.GetName()),
			expectedAppName: plds.integration.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ScaleSubresourceScaledUp(plds.integration)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ScaleSubresourceScaledDown(plds.integration)
			},
		}
	},
	"KameletBinding": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with nested controllers (KameletBinding -> Deployment -> ReplicaSet -> Pod) here,
			// so the pod's owner is ReplicaSet but the expected scaled app is the top-parent KameletBinding CR.
			podOwnerName:    fmt.Sprintf("%s-deployment-replicaset", plds.kameletBinding.GetName()),
			expectedAppName: plds.kameletBinding.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ScaleSubresourceScaledUp(plds.kameletBinding)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ScaleSubresourceScaledDown(plds.kameletBinding)
			},
		}
	},
	"ReplicaSet": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.replicaSet.Name,
			expectedAppName: plds.replicaSet.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ReplicaSetScaledUp(plds.replicaSet)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ReplicaSetScaledDown(plds.replicaSet)
			},
		}
	},
	"DaemonSet": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.daemonSet.Name,
			expectedAppName: plds.daemonSet.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DaemonSetExists(plds.daemonSet)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DaemonSetDoesNotExist(plds.daemonSet)
			},
		}
	},
	"StatefulSet": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.statefulSet.Name,
			expectedAppName: plds.statefulSet.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.StatefulSetScaledUp(plds.statefulSet)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.StatefulSetScaledDown(plds.statefulSet)
			},
		}
	},
	"DeploymentConfig": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with a nested controllers (DeploymentConfig -> ReplicationController -> Pod) here,
			// so we the pod's owner is ReplicaSet but the expected scaled app is the parent Deployment.
			podOwnerName:    fmt.Sprintf("%s-replicationcontroller", plds.deploymentConfig.Name),
			expectedAppName: plds.deploymentConfig.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DeploymentConfigScaledUp(plds.deploymentConfig)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DeploymentConfigScaledDown(plds.deploymentConfig)
			},
		}
	},
	"ReplicationController": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.replicationController.Name,
			expectedAppName: plds.replicationController.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ReplicationControllerScaledUp(plds.replicationController)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.ReplicationControllerScaledDown(plds.replicationController)
			},
		}
	},
	"Job": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.job.Name,
			expectedAppName: plds.job.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.JobExists(plds.job)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.JobDoesNotExist(plds.job)
			},
		}
	},
	"DataVolume": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with nested controllers (DataVolume -> PersistentVolumeClaim -> Pod) here,
			// so the pod's owner is PersistentVolumeClaim but the expected scaled app is the parent DataVolume.
			podOwnerName:    fmt.Sprintf("%s-pvc", plds.dataVolume.GetName()),
			expectedAppName: plds.dataVolume.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DataVolumeExists(plds.dataVolume)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.DataVolumeDoesNotExist(plds.dataVolume)
			},
		}
	},
	"PersistentVolumeClaim": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.persistentVolumeClaim.Name,
			expectedAppName: plds.persistentVolumeClaim.Name,
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.PersistentVolumeClaimExists(plds.persistentVolumeClaim)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.PersistentVolumeClaimDoesNotExist(plds.persistentVolumeClaim)
			},
		}
	},
	"VirtualMachine": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			podOwnerName:    plds.virtualmachineinstance.GetName(),
			expectedAppName: plds.virtualmachine.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.VMRunning(plds.vmStopCallCounter)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.VMStopped(plds.vmStopCallCounter)
			},
		}
	},
	"AnsibleAutomationPlatform": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with nested controllers (AnsibleAutomationPlatform -> Deployment -> ReplicaSet -> Pod) here,
			// so the pod's owner is ReplicaSet but the expected scaled app is the top-parent AnsibleAutomationPlatform CR.
			podOwnerName:    fmt.Sprintf("%s-deployment-replicaset", plds.aap.GetName()),
			expectedAppName: plds.aap.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.AAPRunning(plds.aap)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.AAPIdled(plds.aap)
			},
		}
	},
	"ServingRuntime": func(plds payloads) payloadTestConfig {
		return payloadTestConfig{
			// We are testing the case with nested controllers (ServingRuntime -> Deployment -> ReplicaSet -> Pod) here,
			// so the pod's owner is ReplicaSet but the expected top-parent is ServingRuntime CR. In addition to that,
			// the expected (not-)deleted CR is InferenceService.
			podOwnerName:    fmt.Sprintf("%s-deployment-replicaset", plds.servingRuntime.GetName()),
			expectedAppName: plds.servingRuntime.GetName(),
			ownerScaledUp: func(assertion *test.IdleablePayloadAssertion) {
				assertion.InferenceServiceExists(plds.inferenceService)
			},
			ownerScaledDown: func(assertion *test.IdleablePayloadAssertion) {
				assertion.InferenceServiceDoesNotExist(plds.inferenceService)
			},
		}
	},
}

var customListKinds = map[schema.GroupVersionResource]string{
	{Group: "serving.kserve.io", Version: "v1beta1", Resource: "inferenceservices"}: "InferenceServiceList",
}

func TestAppNameTypeForControllers(t *testing.T) {
	setup := func(t *testing.T, createTestConfig createTestConfigFunc, extraExceedTimeout bool) (*ownerIdler, *test.FakeClientSet, payloadTestConfig, payloads, *corev1.Pod) {
		// Register custom list kinds for KServe resources
		dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, customListKinds)
		fakeDiscovery := newFakeDiscoveryClient(allResourcesList(t)...)
		scalesClient := &fakescale.FakeScaleClient{}
		restClient, err := testcommon.NewRESTClient("dummy-token", apiEndpoint)
		require.NoError(t, err)
		restClient.Client.Transport = gock.DefaultTransport
		t.Cleanup(func() {
			gock.OffAll()
		})

		timeoutSeconds := int32(3600) // 1 hour
		idler := &toolchainv1alpha1.Idler{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-idler",
			},
			Spec: toolchainv1alpha1.IdlerSpec{
				TimeoutSeconds: timeoutSeconds,
			},
		}
		ownerIdler := &ownerIdler{
			idler:         idler,
			ownerFetcher:  owners.NewOwnerFetcher(fakeDiscovery, dynamicClient),
			dynamicClient: dynamicClient,
			scalesClient:  scalesClient,
			restClient:    restClient,
		}

		// Calculate start time based on whether timeout should be exceeded
		timeoutRatio := 1.01
		if extraExceedTimeout {
			timeoutRatio = 1.1
		}

		startTimes := payloadStartTimes{
			defaultStartTime: time.Now().Add(-time.Duration(float64(timeoutSeconds)*timeoutRatio) * time.Second),
			vmStartTime:      time.Now().Add(-time.Duration(float64(timeoutSeconds)/12*timeoutRatio) * time.Second),
		}

		plds := preparePayloads(t, &test.FakeClientSet{DynamicClient: dynamicClient, AllNamespacesClient: testcommon.NewFakeClient(t)}, "alex-stage", "", startTimes)
		tc := createTestConfig(plds)

		p := plds.getFirstControlledPod(tc.podOwnerName)
		return ownerIdler, &test.FakeClientSet{
			DynamicClient:       dynamicClient,
			AllNamespacesClient: testcommon.NewFakeClient(t),
			ScalesClient:        scalesClient,
		}, tc, plds, p
	}

	t.Run("success", func(t *testing.T) {

		for kind, createTestConfig := range testConfigs {
			t.Run(kind, func(t *testing.T) {
				//given
				ownerIdler, fakeClients, testConfig, plds, pod := setup(t, createTestConfig, false)

				//when
				appType, appName, err := ownerIdler.scaleOwnerToZero(context.TODO(), pod)

				//then
				require.NoError(t, err)
				require.Equal(t, kind, appType)
				require.Equal(t, testConfig.expectedAppName, appName)
				assertion := test.AssertThatInIdleableCluster(t, fakeClients)
				testConfig.ownerScaledDown(assertion)
				for otherKind, othersTCFunc := range testConfigs {
					if kind != otherKind {
						othersTCFunc(plds).ownerScaledUp(assertion)
					}
				}
				assertOtherOwners(t, ownerIdler, pod, false)
			})
		}
	})

	t.Run("timeout exceeded - multiple owners processed", func(t *testing.T) {
		for kind, createTestConfig := range testConfigs {
			t.Run(kind, func(t *testing.T) {
				//given - pod running for more than 105% of timeout
				ownerIdler, fakeClients, testConfig, _, pod := setup(t, createTestConfig, true)

				//when
				appType, appName, err := ownerIdler.scaleOwnerToZero(context.TODO(), pod)

				//then
				require.NoError(t, err)
				require.Equal(t, kind, appType)
				require.Equal(t, testConfig.expectedAppName, appName)
				assertion := test.AssertThatInIdleableCluster(t, fakeClients)
				testConfig.ownerScaledDown(assertion)

				// when there are multiple owners, then verify that the second one is scaled down too
				assertOtherOwners(t, ownerIdler, pod, true)
			})
		}
	})

	t.Run("failure when patching/deleting", func(t *testing.T) {
		for kind, createTestConfig := range testConfigs {
			t.Run(kind, func(t *testing.T) {
				//given
				ownerIdler, fakeClients, testConfig, plds, pod := setup(t, createTestConfig, false)
				gock.OffAll()
				// mock stop call
				mockStopVMCalls(".*", ".*", http.StatusInternalServerError)

				affectedKind := kind
				if kind == "ServingRuntime" {
					affectedKind = "InferenceService"
				}
				errMsg := "can't update/delete " + affectedKind
				fakeClients.DynamicClient.PrependReactor("patch", strings.ToLower(affectedKind)+"s", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New(errMsg)
				})
				fakeClients.DynamicClient.PrependReactor("delete", strings.ToLower(affectedKind)+"s", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.New(errMsg)
				})
				fakeClients.ScalesClient.PrependReactor("patch", strings.ToLower(affectedKind)+"s", func(rawAction clienttest.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New(errMsg)
				})

				//when
				appType, appName, err := ownerIdler.scaleOwnerToZero(context.TODO(), pod)

				//then
				fakeClients.ScalesClient.ClearActions()
				assertion := test.AssertThatInIdleableCluster(t, fakeClients)
				if kind != "VirtualMachine" {
					require.EqualError(t, err, errMsg)
					testConfig.ownerScaledUp(assertion)
				} else {
					require.EqualError(t, err, "an error on the server (\"\") has prevented the request from succeeding (put virtualmachines.authentication.k8s.io alex-stage-virtualmachine)")
				}

				require.Equal(t, kind, appType)
				require.Equal(t, testConfig.expectedAppName, appName)
				for otherKind, othersTCFunc := range testConfigs {
					if kind != otherKind {
						othersTCFunc(plds).ownerScaledUp(assertion)
					}
				}
				assertOtherOwners(t, ownerIdler, pod, true)
			})
		}
	})

	t.Run("error when getting owner deployment is ignored", func(t *testing.T) {
		// given
		ownerIdler, fakeClients, testConfig, plds, pod := setup(t, testConfigs["Deployment"], false)
		reactionChain := fakeClients.DynamicClient.ReactionChain
		fakeClients.DynamicClient.PrependReactor("get", "deployments", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("can't get deployment")
		})

		//when
		appType, appName, err := ownerIdler.scaleOwnerToZero(context.TODO(), pod)

		// then
		require.NoError(t, err) // errors are ignored!
		fakeClients.DynamicClient.ReactionChain = reactionChain
		payloadAssertion := test.AssertThatInIdleableCluster(t, fakeClients).
			DeploymentScaledUp(plds.deployment) // deployment is not idled
		for _, rs := range plds.replicaSetsWithDeployment {
			if rs.Name == testConfig.podOwnerName {
				payloadAssertion.ReplicaSetScaledDown(rs) // but the ReplicaSet is
			}
		}
		require.Equal(t, "ReplicaSet", appType)
		require.Equal(t, testConfig.podOwnerName, appName)
	})

	t.Run("owners that are being deleted are skipped", func(t *testing.T) {
		for kind, createTestConfig := range testConfigs {
			t.Run(kind, func(t *testing.T) {
				//given
				ownerIdler, fakeClients, _, _, pod := setup(t, createTestConfig, false)
				owners, err := ownerIdler.ownerFetcher.GetOwners(context.TODO(), pod)
				if !apierrors.IsNotFound(err) {
					require.NoError(t, err)
				}
				// mark the first owner as already being deleted
				if len(owners) != 0 {
					topOwner := owners[0].Object
					now := metav1.Now()
					topOwner.SetDeletionTimestamp(&now)
					// we need to set dummy finalizer so it stays in the fake client and doesn't get deleted
					util.AddFinalizer(topOwner, "dummy-finalizer")
					_, err := fakeClients.DynamicClient.
						Resource(*owners[0].GVR).
						Namespace(topOwner.GetNamespace()).
						Update(context.TODO(), topOwner, metav1.UpdateOptions{})
					require.NoError(t, err)
				}

				//when
				appType, appName, err := ownerIdler.scaleOwnerToZero(context.TODO(), pod)

				//then
				require.NoError(t, err)
				// when there is more than one owner, then it should try to idle
				// the second known owner (we don't support VirtualMachineInstance so we skip this one)
				// in all other cases, there is nothing to idle, so it will return empty string
				// which would mean that the controller should delete the pod
				if len(owners) > 1 && kind != "VirtualMachine" {
					require.Equal(t, owners[1].Object.GetKind(), appType)
					require.Equal(t, owners[1].Object.GetName(), appName)
				} else {
					require.Empty(t, appType)
					require.Empty(t, appName)
				}

				// let's verify that the second owner is scaled down
				assertOtherOwners(t, ownerIdler, pod, true)
			})
		}
	})
}

func assertOtherOwners(t *testing.T, ownerIdler *ownerIdler, pod *corev1.Pod, secondOwnerIdled bool) {
	owners, err := ownerIdler.ownerFetcher.GetOwners(context.TODO(), pod)
	if !apierrors.IsNotFound(err) {
		require.NoError(t, err)
	}
	// if there are more owners than one
	if len(owners) > 1 {
		// by default, all other owners shouldn't be idled
		notIdledOwnersStartIndex := 1
		if secondOwnerIdled {
			// if the second owner is supposed to be idled
			assertReplicas(t, owners[1].Object, 0)
			// then set the start index for all other owners not idled at 2
			notIdledOwnersStartIndex = 2
		}
		// check that all other owners are not idled
		for i := notIdledOwnersStartIndex; i < len(owners)-1; i++ {
			assertReplicas(t, owners[i].Object, 3)
		}
	}
}

func assertReplicas(t *testing.T, object *unstructured.Unstructured, expReplicas int64) {
	replicas, _, err := unstructured.NestedInt64(object.UnstructuredContent(), "spec", "replicas")
	require.NoError(t, err)
	require.Equal(t, expReplicas, replicas)
}

type fakeDiscoveryClient struct {
	*fake.FakeDiscovery
	ServerPreferredResourcesError error
}

func newFakeDiscoveryClient(resources ...*metav1.APIResourceList) *fakeDiscoveryClient {
	fakeDiscovery := fakeclientset.NewSimpleClientset().Discovery().(*fake.FakeDiscovery)
	fakeDiscovery.Resources = resources
	return &fakeDiscoveryClient{
		FakeDiscovery: fakeDiscovery,
	}
}

func (c *fakeDiscoveryClient) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return c.Resources, c.ServerPreferredResourcesError
}

func noAAPResourceList(t *testing.T) []*metav1.APIResourceList {
	require.NoError(t, apis.AddToScheme(scheme.Scheme))
	noAAPResources := []*metav1.APIResourceList{
		{
			GroupVersion: "kubevirt.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "virtualmachineinstances", Namespaced: true, Kind: "VirtualMachineInstance"},
				{Name: "virtualmachines", Namespaced: true, Kind: "VirtualMachine"},
			},
		},
		{
			GroupVersion: "cdi.kubevirt.io/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "datavolumes", Namespaced: true, Kind: "DataVolume"},
			},
		},
	}
	for gvk := range scheme.Scheme.AllKnownTypes() {
		resource, _ := meta.UnsafeGuessKindToResource(gvk)
		noAAPResources = append(noAAPResources, &metav1.APIResourceList{
			GroupVersion: gvk.GroupVersion().String(),
			APIResources: []metav1.APIResource{
				{Name: resource.Resource, Namespaced: true, Kind: gvk.Kind},
			},
		})
	}

	for gvk, gvr := range supportedScaleResources {
		noAAPResources = append(noAAPResources, &metav1.APIResourceList{
			GroupVersion: gvr.GroupVersion().String(),
			APIResources: []metav1.APIResource{
				{Name: gvr.Resource, Namespaced: true, Kind: gvk.Kind},
			},
		})
	}
	return noAAPResources
}

func allResourcesList(t *testing.T) []*metav1.APIResourceList {
	return append(noAAPResourceList(t),
		&metav1.APIResourceList{
			GroupVersion: "aap.ansible.com/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "ansibleautomationplatforms", Namespaced: true, Kind: "AnsibleAutomationPlatform"},
				{Name: "ansibleautomationplatformbackups", Namespaced: true, Kind: "AnsibleAutomationPlatformBackup"},
			},
		},
		&metav1.APIResourceList{
			GroupVersion: "serving.kserve.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "servingruntimes", Namespaced: true, Kind: "ServingRuntime"},
			},
		},
		&metav1.APIResourceList{
			GroupVersion: "serving.kserve.io/v1beta1",
			APIResources: []metav1.APIResource{
				{Name: "inferenceservices", Namespaced: true, Kind: "InferenceService"},
			},
		},
	)
}
