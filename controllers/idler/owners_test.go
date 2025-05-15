package idler

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestGetAPIResourceList(t *testing.T) {
	// given
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"}}
	dynamicClient := fakedynamic.NewSimpleDynamicClient(scheme.Scheme)

	t.Run("get APIs, pod with no owners", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
		fetcher := newOwnerFetcher(fakeDiscovery, dynamicClient)

		// when
		owners, err := fetcher.getOwners(context.TODO(), pod)

		// then
		require.NoError(t, err)
		require.NotEmpty(t, fetcher.resourceLists)
		require.Empty(t, owners)

		t.Run("no APIs retrival when once done", func(t *testing.T) {
			// given
			fakeDiscovery.ServerPreferredResourcesError = fmt.Errorf("some error")

			// when
			owners, err := fetcher.getOwners(context.TODO(), pod)

			// then
			require.NoError(t, err)
			require.NotEmpty(t, fetcher.resourceLists)
			require.Empty(t, owners)
		})
	})

	t.Run("failure when getting APIs", func(t *testing.T) {
		// given
		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)
		fakeDiscovery.ServerPreferredResourcesError = fmt.Errorf("some error")
		fetcher := newOwnerFetcher(fakeDiscovery, dynamicClient)

		// when
		owners, err := fetcher.getOwners(context.TODO(), pod)

		// then
		require.EqualError(t, err, "some error")
		require.Nil(t, fetcher.resourceLists)
		require.Empty(t, owners)
	})
}

func newVMResources(t *testing.T, name, namespace string) (*unstructured.Unstructured, *unstructured.Unstructured) {
	vm := &unstructured.Unstructured{}
	err := vm.UnmarshalJSON(virtualmachineJSON)
	require.NoError(t, err)
	vm.SetNamespace(namespace)
	vm.SetName(name)

	vmi := &unstructured.Unstructured{}
	err = vmi.UnmarshalJSON(virtualmachineinstanceJSON)
	require.NoError(t, err)
	vmi.SetNamespace(namespace)
	vmi.SetName(name)
	return vm, vmi
}

func TestGetOwners(t *testing.T) {
	// given
	givenPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"}}
	replica := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "test-replica", Namespace: "test-namespace"}}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "test-deployment", Namespace: "test-namespace"}}
	aap := newAAP(t, false, "test-aap", "test-namespace")
	vm, vmi := newVMResources(t, "test-vm", "test-namespace")

	testCases := map[string]struct {
		expectedOwners []client.Object
	}{
		"with replica as owner": {
			expectedOwners: []client.Object{replica},
		},
		"with deployment & replica as owners": {
			expectedOwners: []client.Object{deployment, replica},
		},
		"with aap, deployment & replica as owners": {
			expectedOwners: []client.Object{aap, deployment, replica},
		},
		"with vm, vmi, deployment & replica as owners": {
			expectedOwners: []client.Object{vm, vmi, deployment, replica},
		},
	}
	for testName, testData := range testCases {
		t.Run(testName, func(t *testing.T) {
			// given
			pod := givenPod.DeepCopy()
			initObjects := []runtime.Object{pod}
			var noiseObjects []runtime.Object
			for i := len(testData.expectedOwners) - 1; i >= 0; i-- {
				owner := testData.expectedOwners[i].DeepCopyObject().(client.Object)

				noise := owner.DeepCopyObject().(client.Object)
				noise.SetName("noise-" + noise.GetName())
				noiseObjects = append(noiseObjects, noise)

				err := controllerruntime.SetControllerReference(owner, initObjects[len(initObjects)-1].(client.Object), scheme.Scheme)
				require.NoError(t, err)
				initObjects = append(initObjects, owner)
			}

			// owner refs that are not controllers are ignored
			noiseTopOwner := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "test-daemon", Namespace: "test-namespace"}}
			err := controllerutil.SetOwnerReference(noiseTopOwner, initObjects[len(initObjects)-1].(client.Object), scheme.Scheme)
			require.NoError(t, err)

			dynamicClient := fakedynamic.NewSimpleDynamicClient(scheme.Scheme, slices.Concat(initObjects, noiseObjects)...)

			fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
			fetcher := newOwnerFetcher(fakeDiscovery, dynamicClient)

			// when
			owners, err := fetcher.getOwners(context.TODO(), pod)

			// then
			require.NoError(t, err)
			require.Len(t, owners, len(testData.expectedOwners))
			for i := range testData.expectedOwners {
				assert.Equal(t, testData.expectedOwners[i].GetName(), owners[i].object.GetName())
			}

		})
	}
}

func TestGetOwnersFailures(t *testing.T) {
	// given
	givenPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"}}
	replica := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "test-replica", Namespace: "test-namespace"}}
	aap := newAAP(t, false, "test-aap", "test-namespace")

	t.Run("api not available", func(t *testing.T) {
		// given
		pod := givenPod.DeepCopy()
		err := controllerruntime.SetControllerReference(aap, pod, scheme.Scheme)
		require.NoError(t, err)
		dynamicClient := fakedynamic.NewSimpleDynamicClient(scheme.Scheme, pod, aap)

		fakeDiscovery := newFakeDiscoveryClient(noAAPResourceList(t)...)
		fetcher := newOwnerFetcher(fakeDiscovery, dynamicClient)

		// when
		owners, err := fetcher.getOwners(context.TODO(), pod)

		// then
		require.EqualError(t, err, "no resource found for kind AnsibleAutomationPlatform in aap.ansible.com/v1alpha1")
		require.Nil(t, owners)
	})

	t.Run("replica not found", func(t *testing.T) {
		// given
		pod := givenPod.DeepCopy()
		err := controllerruntime.SetControllerReference(replica, pod, scheme.Scheme)
		require.NoError(t, err)
		dynamicClient := fakedynamic.NewSimpleDynamicClient(scheme.Scheme, pod)

		fakeDiscovery := newFakeDiscoveryClient(withAAPResourceList(t)...)
		fetcher := newOwnerFetcher(fakeDiscovery, dynamicClient)

		// when
		owners, err := fetcher.getOwners(context.TODO(), pod)

		// then
		require.EqualError(t, err, "replicasets.apps \"test-replica\" not found")
		require.Nil(t, owners)
	})
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

var aapGVK = map[schema.GroupVersionResource]string{
	{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatforms"}:       "AnsibleAutomationPlatformList",
	{Group: "aap.ansible.com", Version: "v1alpha1", Resource: "ansibleautomationplatformbackups"}: "AnsibleAutomationPlatformBackupList",
}
