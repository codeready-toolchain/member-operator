package idler

import (
	"context"
	"fmt"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clienttest "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestNewAAPIdler(t *testing.T) {
	t.Run("with AAP API available", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, idler.resourceLists)
		assert.NotEmpty(t, idler.aapGVR)
	})

	t.Run("without AAP API", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, idler.resourceLists)
		assert.Nil(t, idler.aapGVR)
	})

	t.Run("with AAPBackup API only", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(append(noAAPResourceList(t), &metav1.APIResourceList{
			GroupVersion: "aap.ansible.com/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "ansibleautomationplatformbackups", Namespaced: true, Kind: "AnsibleAutomationPlatformBackup"},
			},
		})...)

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, idler.resourceLists)
		assert.Nil(t, idler.aapGVR)
	})

	t.Run("failure", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)
		fakeDiscovery.ServerPreferredResourcesError = fmt.Errorf("some error")

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.EqualError(t, err, "some error")
		require.Nil(t, idler)
	})
}

func TestAAPIdler(t *testing.T) {
	// given
	idler := &toolchainv1alpha1.Idler{
		ObjectMeta: metav1.ObjectMeta{
			Name: "john-dev",
		},
		Spec: toolchainv1alpha1.IdlerSpec{TimeoutSeconds: TestIdlerTimeOutSeconds},
	}
	idledAAP := newAAP(t, true, "idled-test", idler.Name)
	runningAAP := newAAP(t, false, "running-test", idler.Name)
	noiseAAP := newAAP(t, false, "running-noise", "noise-dev")
	runningNoSpecAAP := newNoSpecAAP(t, "running-no-spec", idler.Name)

	t.Run("with idled AAP only", func(t *testing.T) {
		// given
		aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, noiseAAP)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Empty(t, requeueAfter)
		interceptedNotify.assertThatCalled(t)
		assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
	})

	t.Run("AAP API not available", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)
		aapIdler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)
		require.NoError(t, err)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Empty(t, requeueAfter)
	})

	t.Run("with running AAPs, but no pod owned by the CRs", func(t *testing.T) {
		// given
		aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assertRequeueAfterNoisePodsAAPTimeout(t, requeueAfter, idler)
		interceptedNotify.assertThatCalled(t)
		assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
	})

	t.Run("with running AAPs, nothing to idle, pods owned by AAP are short-lived", func(t *testing.T) {
		// given
		aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)

		isOwningSomething := false
		preparePayloadsForAAPIdler(t, aapIdler, func(gvk schema.GroupVersionKind, object client.Object) {
			if gvk.Kind == "Deployment" {
				// we want the AAP instance to own only one Deployment - that would be enough to idle it
				if !isOwningSomething {
					require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
					isOwningSomething = true
				}
			}
		}, idler.Name, "short-", freshStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

		preparePayloadCrashloopingPodsWithinThreshold(t, clientSetForIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
			require.NoError(t, controllerutil.SetOwnerReference(runningNoSpecAAP, object, scheme.Scheme))
		}), idler.Name, "restarting-", freshStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assertRequeueTimeInDelta(t, requeueAfter, aapTimeoutSeconds(idler.Spec.TimeoutSeconds)/2)
		interceptedNotify.assertThatCalled(t)
		assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
	})

	t.Run("with running AAPs, one (long-running) is idled, second (short-lived) is scheduled", func(t *testing.T) {
		// given
		aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)
		isOwningSomething := false
		preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
			if kind.Kind == "Deployment" {
				// we want the AAP instance to own only one Deployment - that will be enough to idle it
				if !isOwningSomething {
					require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
					isOwningSomething = true
				}
			}
		}, idler.Name, "long-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))
		preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
			if kind.Kind == "ReplicaSet" && len(object.GetOwnerReferences()) == 0 {
				require.NoError(t, controllerutil.SetOwnerReference(runningNoSpecAAP, object, scheme.Scheme))
			}
		}, idler.Name, "short-running-", freshStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assertRequeueTimeInDelta(t, requeueAfter, aapTimeoutSeconds(idler.Spec.TimeoutSeconds)/2)
		interceptedNotify.assertThatCalled(t, runningAAP.GetName())
		assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName(), runningAAP.GetName())
	})

	t.Run("both long-running and restarting APPs are idled", func(t *testing.T) {
		// given
		aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, noiseAAP, runningNoSpecAAP)
		preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
			if kind.Kind == "Deployment" {
				// let's make the AAP owner of all Deployments, to check that everything works as expected
				require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
			}
		}, idler.Name, "long-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

		preparePayloadCrashloopingAboveThreshold(t, clientSetForIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
			require.NoError(t, controllerutil.SetOwnerReference(runningNoSpecAAP, object, scheme.Scheme))
		}), idler.Name, "restarting-")

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Empty(t, requeueAfter)
		interceptedNotify.assertThatCalled(t, runningAAP.GetName(), runningNoSpecAAP.GetName())
		assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName(), runningAAP.GetName(), runningNoSpecAAP.GetName())
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("list pods fails", func(t *testing.T) {
			// given
			aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)
			aapIdler.allNamespacesClient.(*test.FakeClient).MockList = func(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
				return fmt.Errorf("some errror")
			}
			preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
				if kind.Kind == "Deployment" {
					require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
				}
			}, idler.Name, "long-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

			// when
			requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

			// then
			require.EqualError(t, err, "some errror")
			assert.Empty(t, requeueAfter)
			interceptedNotify.assertThatCalled(t)
			assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
		})

		t.Run("for dynamic client", func(t *testing.T) {
			// given
			aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)
			preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
				if kind.Kind == "Deployment" {
					require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
				}
			}, idler.Name, "long-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

			dynamicCl := aapIdler.dynamicClient.(*fakedynamic.FakeDynamicClient)
			originalReactions := make([]clienttest.Reactor, len(dynamicCl.ReactionChain))
			copy(originalReactions, dynamicCl.ReactionChain)

			t.Run("get deployment fails", func(t *testing.T) {
				// given
				dynamicCl.Fake.ReactionChain = originalReactions
				dynamicCl.PrependReactor("get", "deployments", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, fmt.Errorf("some get error")
				})

				// when
				requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

				// then
				require.ErrorContains(t, err, "some get error")
				assertRequeueAfterNoisePodsAAPTimeout(t, requeueAfter, idler)
				interceptedNotify.assertThatCalled(t)
				assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
			})

			t.Run("list AAP fails", func(t *testing.T) {
				// given
				dynamicCl.Fake.ReactionChain = originalReactions
				dynamicCl.PrependReactor("list", "ansibleautomationplatforms", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, fmt.Errorf("some list error")
				})

				// when
				requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

				// then
				require.EqualError(t, err, "some list error")
				assert.Empty(t, requeueAfter)
				interceptedNotify.assertThatCalled(t)
				dynamicCl.Fake.ReactionChain = originalReactions
				assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
			})

			t.Run("patch AAP fails", func(t *testing.T) {
				// given
				aapIdler, interceptedNotify := prepareAAPIdler(t, idler, idledAAP, runningAAP, runningNoSpecAAP, noiseAAP)
				preparePayloadsForAAPIdler(t, aapIdler, func(kind schema.GroupVersionKind, object client.Object) {
					if kind.Kind == "Deployment" {
						require.NoError(t, controllerutil.SetOwnerReference(runningAAP, object, scheme.Scheme))
					}
				}, idler.Name, "long-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))

				// there will be multiple pods/deployments owned by the AAP, but let's return an error only once
				errReturned := false
				dynamicCl := aapIdler.dynamicClient.(*fakedynamic.FakeDynamicClient)
				dynamicCl.PrependReactor("patch", "ansibleautomationplatforms", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					if !errReturned {
						errReturned = true
						return true, nil, fmt.Errorf("some patch error")
					}
					return false, nil, nil
				})

				// when
				requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

				// then
				// the dynamic client returned an error only once, but since the AAP instance owned several deployments,
				// then the AAP was idled and the user was also notified
				require.EqualError(t, err, "some patch error")
				assertRequeueAfterNoisePodsAAPTimeout(t, requeueAfter, idler)
				interceptedNotify.assertThatCalled(t, runningAAP.GetName())
				assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName(), runningAAP.GetName())
			})

			t.Run("with not found for deployment owner", func(t *testing.T) {
				dynamicCl.PrependReactor("get", "deployments", func(action clienttest.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, errors.NewNotFound(schema.GroupResource{}, "Deployment")
				})

				// when
				requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

				// then
				require.NoError(t, err)
				assertRequeueAfterNoisePodsAAPTimeout(t, requeueAfter, idler)
				interceptedNotify.assertThatCalled(t)
				assertAAPsIdled(t, aapIdler, idler.Name, idledAAP.GetName())
			})
		})

	})

}

func assertRequeueAfterNoisePodsAAPTimeout(t *testing.T, requeueAfter time.Duration, idler *toolchainv1alpha1.Idler) {
	baseLineSeconds := aapTimeoutSeconds(idler.Spec.TimeoutSeconds) - aapTimeoutSeconds(idler.Spec.TimeoutSeconds)/12
	assertRequeueTimeInDelta(t, requeueAfter, baseLineSeconds)
}

const oneHour = int32(60 * 60)

func TestAAPTimeoutSeconds(t *testing.T) {
	assert.Equal(t, 2*oneHour, aapTimeoutSeconds(3*oneHour))
	assert.Equal(t, oneHour+1, aapTimeoutSeconds(2*oneHour+1))
	assert.Equal(t, oneHour, aapTimeoutSeconds(2*oneHour))
	assert.Equal(t, oneHour-1, aapTimeoutSeconds(2*oneHour-1))
	assert.Equal(t, oneHour/2, aapTimeoutSeconds(oneHour))
}

func assertAAPsIdled(t *testing.T, aapIdler *aapIdler, namespace string, names ...string) {
	aapList, err := aapIdler.dynamicClient.Resource(*aapIdler.aapGVR).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)

	var idledAAPs []string
	for _, aap := range aapList.Items {
		idled, _, err := unstructured.NestedBool(aap.UnstructuredContent(), "spec", "idle_aap")
		require.NoError(t, err)
		if idled {
			idledAAPs = append(idledAAPs, aap.GetName())
		}
	}
	assert.ElementsMatch(t, names, idledAAPs)
}

type notifyUserInterceptor struct {
	aapNameCalled map[string]int
}

func (i *notifyUserInterceptor) interceptAAPCalls(t *testing.T) notifyFunc {
	return func(ctx context.Context, idler *toolchainv1alpha1.Idler, appName string, appType string) {
		assert.Equal(t, "Ansible Automation Platform", appType)
		if i.aapNameCalled == nil {
			i.aapNameCalled = make(map[string]int)
		}
		i.aapNameCalled[appName]++
	}
}

func (i *notifyUserInterceptor) assertThatCalled(t *testing.T, aapNames ...string) {
	if len(aapNames) == 0 {
		assert.Empty(t, i.aapNameCalled)
	} else {
		require.NotEmpty(t, i.aapNameCalled)
		require.Len(t, i.aapNameCalled, len(aapNames))
		for _, aapName := range aapNames {
			require.Contains(t, i.aapNameCalled, aapName)
			assert.Equal(t, 1, i.aapNameCalled[aapName])
		}
	}
}

func prepareAAPIdler(t *testing.T, idler *toolchainv1alpha1.Idler, initObjects ...runtime.Object) (*aapIdler, *notifyUserInterceptor) {
	require.NoError(t, apis.AddToScheme(scheme.Scheme))
	fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
	allNamespacesClient := test.NewFakeClient(t)
	dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, aapGVK, initObjects...)

	interceptor := &notifyUserInterceptor{}
	aapIdler, err := newAAPIdler(allNamespacesClient, dynamicClient, fakeDiscovery, interceptor.interceptAAPCalls(t))
	require.NoError(t, err)

	preparePayloadsForAAPIdler(t, aapIdler, nil, idler.Name, "noise-", expiredStartTimes(aapTimeoutSeconds(idler.Spec.TimeoutSeconds)))
	preparePayloadCrashloopingAboveThreshold(t, clientSetForIdler(t, aapIdler, nil), idler.Name, "restarting-noise-")
	preparePayloadsForAAPIdler(t, aapIdler, nil, idler.Name, "no-start-time-", payloadStartTimes{})

	return aapIdler, interceptor
}

type adjustOwnership func(kind schema.GroupVersionKind, object client.Object)

func preparePayloadsForAAPIdler(t *testing.T, aapIdler *aapIdler, updateObject adjustOwnership, namespace, namePrefix string, startTimes payloadStartTimes) {
	preparePayloadsWithCreateFunc(t, clientSetForIdler(t, aapIdler, updateObject), namespace, namePrefix, startTimes)
}

func clientSetForIdler(t *testing.T, aapIdler *aapIdler, updateObject adjustOwnership) clientSet {
	return clientSet{
		allNamespacesClient: aapIdler.allNamespacesClient,
		dynamicClient:       aapIdler.dynamicClient,
		createOwnerObjects: func(ctx context.Context, object client.Object) error {
			return createObjectWithDynamicClient(t, aapIdler.dynamicClient, object, updateObject)
		},
	}
}

func createObjectWithDynamicClient(t *testing.T, dynamicClient dynamic.Interface, object client.Object, updateObject func(kind schema.GroupVersionKind, object client.Object)) error {
	// get GVK and GVR
	kinds, _, err := scheme.Scheme.ObjectKinds(object)
	require.NoError(t, err)
	kind := kinds[0]
	resource, _ := meta.UnsafeGuessKindToResource(kind)

	// do any necessary updates
	if updateObject != nil {
		updateObject(kind, object)
	}
	// convert to unstructured.Unstructured
	object.GetObjectKind().SetGroupVersionKind(kind)
	tmp, err := json.Marshal(object)
	require.NoError(t, err)
	unstructuredObj := &unstructured.Unstructured{}
	err = unstructuredObj.UnmarshalJSON(tmp)
	require.NoError(t, err)
	// create
	_, err = dynamicClient.Resource(resource).Namespace(object.GetNamespace()).Create(context.TODO(), unstructuredObj, metav1.CreateOptions{})
	return err
}

func newAAP(t *testing.T, idled bool, name, namespace string) *unstructured.Unstructured {
	formatted := fmt.Sprintf(aap, name, namespace, idled)
	aap := &unstructured.Unstructured{}
	require.NoError(t, aap.UnmarshalJSON([]byte(formatted)))
	return aap
}

func newNoSpecAAP(t *testing.T, name, namespace string) *unstructured.Unstructured {
	formatted := fmt.Sprintf(aapHeader+"}", name, namespace)
	aap := &unstructured.Unstructured{}
	require.NoError(t, aap.UnmarshalJSON([]byte(formatted)))
	return aap
}

func noAAPResourceList(t *testing.T) []*metav1.APIResourceList {
	require.NoError(t, apis.AddToScheme(scheme.Scheme))
	noAAPResources := []*metav1.APIResourceList{
		{
			GroupVersion: vmGVR.GroupVersion().String(),
			APIResources: []metav1.APIResource{
				{Name: "virtualmachineinstances", Namespaced: true, Kind: "VirtualMachineInstance"},
				{Name: "virtualmachines", Namespaced: true, Kind: "VirtualMachine"},
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

	for gvk, gvr := range SupportedScaleResources {
		noAAPResources = append(noAAPResources, &metav1.APIResourceList{
			GroupVersion: gvr.GroupVersion().String(),
			APIResources: []metav1.APIResource{
				{Name: gvr.Resource, Namespaced: true, Kind: gvk.Kind},
			},
		})
	}
	return noAAPResources
}

func withAAPResourceList(t *testing.T) []*metav1.APIResourceList {
	return append(noAAPResourceList(t), &metav1.APIResourceList{
		GroupVersion: "aap.ansible.com/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "ansibleautomationplatforms", Namespaced: true, Kind: "AnsibleAutomationPlatform"},
			{Name: "ansibleautomationplatformbackups", Namespaced: true, Kind: "AnsibleAutomationPlatformBackup"},
		},
	})
}

var (
	aapGVK = map[schema.GroupVersionResource]string{
		{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatforms"}:       "AnsibleAutomationPlatformList",
		{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatformbackups"}: "AnsibleAutomationPlatformBackupList",
	}

	aapHeader = `{
  "apiVersion": "aap.ansible.com/v1alpha1",
  "kind": "AnsibleAutomationPlatform",
  "metadata": {
    "labels": {
      "app.kubernetes.io/managed-by": "aap-operator"
    },
    "name": "%s",
    "namespace": "%s"
  }`

	aap = aapHeader + `,
  "spec": {
    "eda": {
      "api": {
        "replicas": 1,
        "resource_requirements": {
          "limits": {
            "cpu": "500m"
          }
        }
      }
    },
    "idle_aap": %t,
    "no_log": false
  },
  "status": {
    "conditions": {
      "message": "",
      "reason": "",
      "status": "True",
      "type": "Successful"
    }
  }
}`
)

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
