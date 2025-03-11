package idler

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	aapKind       = "AnsibleAutomationPlatform"
	aapAPIVersion = "aap.ansible.com/v1alpha1"
)

type aapIdler struct {
	allNamespacesClient client.Client
	dynamicClient       dynamic.Interface
	notifyUser          notifyFunc
	aapGVR              *schema.GroupVersionResource // AAP GVR
	resourceLists       []*metav1.APIResourceList    // All available API in the cluster
}

type notifyFunc func(context.Context, *toolchainv1alpha1.Idler, string, string)

func newAAPIdler(allNamespacesClient client.Client, dynamicClient dynamic.Interface, discoveryClient discovery.ServerResourcesInterface, notifyUser notifyFunc) (*aapIdler, error) {
	// Get all API resources from the cluster using the discovery client. We need it for constructing GVRs for unstructured objects.
	// Do it here once, so we do not have to list it multiple times before listing/getting every unstructured resource.
	resourceLists, err := discoveryClient.ServerPreferredResources()
	if err != nil {
		return nil, err
	}

	// Check if the AAP API is even available/installed
	aapGVR, err := findGVRForKind(aapKind, aapAPIVersion, resourceLists)
	if err != nil {
		return nil, err
	}

	return &aapIdler{
		allNamespacesClient: allNamespacesClient,
		dynamicClient:       dynamicClient,
		notifyUser:          notifyUser,
		resourceLists:       resourceLists,
		aapGVR:              aapGVR,
	}, nil
}

// ensureAnsiblePlatformIdling checks if there is any long-running pod belonging to an AAP resource and if yes, then it idles the AAP
// and sends a notification to the user.
func (i *aapIdler) ensureAnsiblePlatformIdling(ctx context.Context, idler *toolchainv1alpha1.Idler) (time.Duration, error) {
	if i.aapGVR == nil {
		return 0, nil // aap api is not available
	}

	// Check if there is any AAP CRs in the namespace
	idledAAPs := make(map[string]string)

	aapList, err := i.dynamicClient.Resource(*i.aapGVR).Namespace(idler.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	running := make([]string, 0, len(aapList.Items))
	for _, aap := range aapList.Items {
		// we don't need to check if the field was found - if not found, then the value is "false" (not idled)
		idled, _, err := unstructured.NestedBool(aap.UnstructuredContent(), "spec", "idle_aap")
		if err != nil {
			return 0, err
		}
		if !idled {
			running = append(running, aap.GetName())
		}
	}

	if len(running) == 0 {
		// No running AAP resource found. Nothing to idle.
		return 0, nil
	}

	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := i.allNamespacesClient.List(ctx, podList, client.InNamespace(idler.Name)); err != nil {
		return 0, err
	}
	timeoutSeconds := aapTimeoutSeconds(idler.Spec.TimeoutSeconds)
	requeueAfter := time.Duration(timeoutSeconds) * time.Second
	for _, pod := range podList.Items {
		startTime := pod.Status.StartTime
		if startTime == nil {
			continue
		}
		var idledAAPName string
		pod := pod // TODO We won't need it after upgrading to go 1.22: https://go.dev/blog/loopvar-preview
		podLogger := log.FromContext(ctx).WithValues("pod_name", pod.Name, "pod_phase", pod.Status.Phase)
		podCtx := log.IntoContext(ctx, podLogger)

		// check the restart count for the pod
		restartCount := getHighestRestartCount(pod.Status)
		if restartCount > aaPRestartThreshold {
			podLogger.Info("Pod is restarting too often for an AAP pod. Checking if it belongs to AAP and if so then idle the aap", "restart_count", restartCount)
			idledAAPName, err = i.ensureAAPIdled(podCtx, pod)
			if err != nil {
				return 0, err
			}
		} else {
			// Check if running for longer then the AAP idler timeout
			if time.Now().After(startTime.Add(time.Duration(timeoutSeconds) * time.Second)) {
				podLogger.Info("Pod is running for too long for an AAP pod. Checking if it belongs to AAP and if so then idle the aap.",
					"start_time", startTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", timeoutSeconds)
				idledAAPName, err = i.ensureAAPIdled(podCtx, pod)
				if err != nil {
					return 0, err
				}
			} else {
				// we don't know if it's an aap pod or not, let's schedule the next reconcile like assuming it is an aap pod to make sure
				// that it would be killed after the timeout
				killAfter := time.Until(startTime.Add(time.Duration(timeoutSeconds+1) * time.Second))
				requeueAfter = shorterDuration(requeueAfter, killAfter)
			}
		}

		// Check if we need to send a notification and proceed with the next pod
		if idledAAPName != "" {
			// The AAP has been Idled
			// A notification should be sent
			i.notifyUser(podCtx, idler, idledAAPName, "Ansible Automation Platform")

			idledAAPs[idledAAPName] = idledAAPName
			if len(idledAAPs) == len(running) {
				// All AAPs are idled, no need to check the rest of the pods
				// no need to schedule any aap-specific requeue
				return 0, nil
			}
		}
	}

	// there is at least one aap instance, schedule the next reconcile
	return requeueAfter, nil
}

const twoHours = 2 * 60 * 60 // in seconds

func aapTimeoutSeconds(idlerTimeout int32) int32 {
	// Check if the idler timeout is less than two hours and if so, set it to half of the timeout.
	// Otherwise, subtract one hour from the timeout.
	// This is to ensure that the AAP idler kicks in before the main idler.
	timeoutSeconds := idlerTimeout
	if timeoutSeconds <= twoHours {
		timeoutSeconds = timeoutSeconds / 2
	} else {
		timeoutSeconds = timeoutSeconds - twoHours/2
	}
	return timeoutSeconds
}

// ensureAAPIdled checks if the long-running or crash-looping pod belongs to an AAP instance and if so, ensures that the AAP is idled.
// Returns the AAP resource name in case it was idled, or an empty string if it was not idled.
func (i *aapIdler) ensureAAPIdled(ctx context.Context, pod corev1.Pod) (string, error) {
	aap, err := i.getAAPOwner(ctx, &pod)
	if err != nil || aap == nil {
		return "", err // either error or no AAP owner found
	}

	// Patch the aap resource by setting spec.idle_aap to true in order to idle it
	patch := []byte(`{"spec":{"idle_aap":true}}`)
	_, err = i.dynamicClient.Resource(*i.aapGVR).Namespace(pod.Namespace).Patch(ctx, aap.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", err
	}

	log.FromContext(ctx).Info("AAP idled", "name", aap.GetName())

	return aap.GetName(), nil
}

// getAAPOwner returns the top level owner of the given object if it is an AAP instance.
func (i *aapIdler) getAAPOwner(ctx context.Context, obj metav1.Object) (metav1.Object, error) {
	owners := obj.GetOwnerReferences()
	var owner metav1.OwnerReference
	if len(owners) == 0 {
		return nil, nil // No owner
	}
	if len(owners) > 1 {
		owner = owners[0] // Multiple owners, use the first one
	}
	logger := log.FromContext(ctx)
	// Get the GVR for the owner
	gvr, err := gvrForKind(owner.Kind, owner.APIVersion, i.resourceLists)
	if err != nil {
		return nil, err
	}
	// Get the owner object
	ownerObject, err := i.dynamicClient.Resource(*gvr).Namespace(obj.GetNamespace()).Get(ctx, owner.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
		logger.Info("Owner not found", "kind", owner.Kind, "name", owner.Name)
		return nil, nil
	}
	if ownerObject.GetKind() == aapKind {
		// Found the top AAP owner. Return it.
		return ownerObject, nil
	}

	// Recursively try to find the top AAP owner
	return i.getAAPOwner(ctx, ownerObject)
}

// gvrForKind returns GVR for the kind, if it's found in the available API list in the cluster
// returns an error if not found or failed to parse the API version
func gvrForKind(kind, apiVersion string, resourceLists []*metav1.APIResourceList) (*schema.GroupVersionResource, error) {
	gvr, err := findGVRForKind(kind, apiVersion, resourceLists)
	if gvr == nil && err == nil {
		return nil, fmt.Errorf("no resource found for kind %s in %s", kind, apiVersion)
	}
	return gvr, err
}

// findGVRForKind returns GVR for the kind, if it's found in the available API list in the cluster
// if not found then returns nil, nil
// returns nil, error if failed to parse the API version
func findGVRForKind(kind, apiVersion string, resourceLists []*metav1.APIResourceList) (*schema.GroupVersionResource, error) {
	// Parse the group and version from the APIVersion (e.g., "apps/v1" -> group: "apps", version: "v1")
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse APIVersion %s: %w", apiVersion, err)
	}

	// Look for a matching resource
	for _, resourceList := range resourceLists {
		if resourceList.GroupVersion == apiVersion {
			for _, apiResource := range resourceList.APIResources {
				if apiResource.Kind == kind {
					// Construct the GVR
					return &schema.GroupVersionResource{
						Group:    gv.Group,
						Version:  gv.Version,
						Resource: apiResource.Name,
					}, nil
				}
			}
		}
	}

	return nil, nil
}
