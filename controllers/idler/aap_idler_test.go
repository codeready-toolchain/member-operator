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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/scale/scheme/appsv1beta2"
)

func TestNewAAPIdler(t *testing.T) {
	t.Run("with AAP API available", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList...)

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, idler.resourceLists)
		require.Len(t, idler.resourceLists, 3)
		assert.NotEmpty(t, idler.aapGVR)
	})

	t.Run("without AAP API", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList...)

		// when
		idler, err := newAAPIdler(nil, nil, fakeDiscovery, nil)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, idler.resourceLists)
		require.Len(t, idler.resourceLists, 2)
		assert.Nil(t, idler.aapGVR)
	})

	t.Run("with AAPBackup API only", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(append(noAAPResourceList, &metav1.APIResourceList{
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
		require.Len(t, idler.resourceLists, 3)
		assert.Nil(t, idler.aapGVR)
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
	idled := newAAP(t, true, "idled-test", idler.Name)
	running := newAAP(t, false, "running-test", idler.Name)
	noise := newAAP(t, false, "running-noise", "noise-dev")
	runningNoSpec := newNoSpecAAP(t, "running-no-spec", idler.Name)

	t.Run("with idled AAP only", func(t *testing.T) {
		// given
		aapIdler := prepareAAPIdler(t, idled, noise)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Empty(t, requeueAfter)
	})

	t.Run("with running AAP", func(t *testing.T) {
		// given
		aapIdler := prepareAAPIdler(t, idled, running, noise)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second/2, requeueAfter)
	})

	t.Run("with running AAP that doesn't contain spec", func(t *testing.T) {
		// given
		aapIdler := prepareAAPIdler(t, idled, runningNoSpec, noise)

		// when
		requeueAfter, err := aapIdler.ensureAnsiblePlatformIdling(context.TODO(), idler)

		// then
		require.NoError(t, err)
		assert.Equal(t, time.Duration(idler.Spec.TimeoutSeconds)*time.Second/2, requeueAfter)
	})
}

func TestAAPTimeoutSeconds(t *testing.T) {
	oneHour := int32(60 * 60)
	assert.Equal(t, 2*oneHour, aapTimeoutSeconds(3*oneHour))
	assert.Equal(t, oneHour+1, aapTimeoutSeconds(2*oneHour+1))
	assert.Equal(t, oneHour, aapTimeoutSeconds(2*oneHour))
	assert.Equal(t, oneHour-1, aapTimeoutSeconds(2*oneHour-1))
	assert.Equal(t, oneHour/2, aapTimeoutSeconds(oneHour))
}

func prepareAAPIdler(t *testing.T, initObjects ...runtime.Object) *aapIdler {
	s := scheme.Scheme
	require.NoError(t, apis.AddToScheme(s))

	allNamespacesClient := test.NewFakeClient(t)
	dynamicClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(s, aapGVK, initObjects...)
	fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList...)

	aapIdler, err := newAAPIdler(allNamespacesClient, dynamicClient, fakeDiscovery, nil)
	require.NoError(t, err)
	return aapIdler
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

var (
	aapGVK = map[schema.GroupVersionResource]string{
		{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatforms"}:       "AnsibleAutomationPlatformList",
		{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatformbackups"}: "AnsibleAutomationPlatformBackupList",
	}

	noAAPResourceList = []*metav1.APIResourceList{
		{
			GroupVersion: corev1.SchemeGroupVersion.String(),
			APIResources: []metav1.APIResource{
				{Name: "pods", Namespaced: true, Kind: "Pod"},
				{Name: "replicationcontrollers", Namespaced: true, Kind: "ReplicationController"},
				{Name: "replicationcontrollers/scale", Namespaced: true, Kind: "Scale", Group: "autoscaling", Version: "v1"},
			},
		},
		{
			GroupVersion: appsv1beta2.SchemeGroupVersion.String(),
			APIResources: []metav1.APIResource{
				{Name: "deployments", Namespaced: true, Kind: "Deployment"},
				{Name: "deployments/scale", Namespaced: true, Kind: "Scale", Group: "apps", Version: "v1beta2"},
			},
		},
	}

	withAAPResourceList = append(noAAPResourceList, &metav1.APIResourceList{
		GroupVersion: "aap.ansible.com/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "ansibleautomationplatforms", Namespaced: true, Kind: "AnsibleAutomationPlatform"},
			{Name: "ansibleautomationplatformbackups", Namespaced: true, Kind: "AnsibleAutomationPlatformBackup"},
		},
	})

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
}

func newFakeDiscoveryClient(resources ...*metav1.APIResourceList) *fakeDiscoveryClient {
	fakeDiscovery := fakeclientset.NewSimpleClientset().Discovery().(*fake.FakeDiscovery)
	fakeDiscovery.Resources = resources
	return &fakeDiscoveryClient{
		FakeDiscovery: fakeDiscovery,
	}
}

func (c *fakeDiscoveryClient) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return c.Resources, nil
}
