package idler

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	aapKind       = "AnsibleAutomationPlatform"
	aapAPIVersion = "aap.ansible.com/v1alpha1"
)

type apiInfo struct {
	aapGVR        schema.GroupVersionResource // AAP GVR
	resourceLists []*metav1.APIResourceList   // All available API in the cluster
}

// ensureAnsiblePlatformIdling checks if there is any long-running pod belonging to an AAP resource and if yes, then it idles the AAP
// and sends a notification to the user.
func (r *Reconciler) ensureAnsiblePlatformIdling(ctx context.Context, idler *toolchainv1alpha1.Idler) (time.Duration, error) {

	// Get all API resources from the cluster using the discovery client. We need it for constructing GVRs for unstructured objects.
	// Do it here once, so we do not have to list it multiple times before listing/getting every unstructured resource.
	resourceLists, err := r.DiscoveryClient.ServerPreferredResources()
	if err != nil {
		return 0, err
	}

	// Check if the AAP API is even available/installed
	aapGVR, err := findGVRForKind(aapKind, aapAPIVersion, resourceLists)
	if err != nil || aapGVR == nil {
		return 0, err
	}
	apiInfo := apiInfo{
		resourceLists: resourceLists,
		aapGVR:        *aapGVR,
	}

	// Check if there is any AAP CRs in the namespace
	idledAAPs := make(map[string]string)
	aapList, err := r.DynamicClient.Resource(apiInfo.aapGVR).Namespace(idler.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	if len(aapList.Items) == 0 {
		// No AAP resources found. Nothing to idle.
		return 0, nil
	}

	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.AllNamespacesClient.List(ctx, podList, client.InNamespace(idler.Name)); err != nil {
		return 0, err
	}
	timeoutSeconds := r.aapTimeoutSeconds(idler)
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
			idledAAPName, err = r.ensureAAPIdled(podCtx, pod, apiInfo)
			if err != nil {
				return 0, err
			}
		} else {
			// Check if running for longer then the AAP idler timeout
			if time.Now().After(startTime.Add(time.Duration(timeoutSeconds) * time.Second)) {
				podLogger.Info("Pod is running for too long for an AAP pod. Checking if it belongs to AAP and if so then idle the aap.",
					"start_time", startTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", timeoutSeconds)
				idledAAPName, err = r.ensureAAPIdled(podCtx, pod, apiInfo)
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
			if err := r.createNotification(podCtx, idler, idledAAPName, "Ansible Automation Platform"); err != nil {
				podLogger.Error(err, "failed to create Notification")
				if err = r.setStatusIdlerNotificationCreationFailed(podCtx, idler, err.Error()); err != nil {
					podLogger.Error(err, "failed to set status IdlerNotificationCreationFailed")
				} // not returning error to continue tracking remaining pods
			}

			idledAAPs[idledAAPName] = idledAAPName
			if len(idledAAPs) == len(aapList.Items) {
				// All AAPs are idled, no need to check the rest of the pods
				// no need to schedule any aap-specific requeue
				return 0, nil
			}
		}
	}

	// there is at least one aap instance, schedule the next reconcile
	return requeueAfter, nil
}

const oneHour = 60 * 60 // in seconds

func (r *Reconciler) aapTimeoutSeconds(idler *toolchainv1alpha1.Idler) int32 {
	// Check if the idler timeout is less than one hour and if so, set it to half of the timeout.
	// Otherwise, subtract one hour from the timeout.
	// This is to ensure that the AAP idler kicks in before the main idler.
	timeoutSeconds := idler.Spec.TimeoutSeconds
	if timeoutSeconds <= oneHour {
		timeoutSeconds = timeoutSeconds / 2
	} else {
		timeoutSeconds = timeoutSeconds - oneHour
	}
	return timeoutSeconds
}

// ensureAAPIdled checks if the long-running or crash-looping pod belongs to an AAP instance and if so, ensures that the AAP is idled.
// Returns the AAP resource name in case it was idled, or an empty string if it was not idled.
func (r *Reconciler) ensureAAPIdled(ctx context.Context, pod corev1.Pod, apiInfo apiInfo) (string, error) {
	aap, err := r.getAAPOwner(ctx, &pod, apiInfo)
	if err != nil || aap == nil {
		return "", err // either error or no AAP owner found
	}

	// Patch the aap resource by setting spec.idle_aap to true in order to idle it
	patch := []byte(`{"spec":{"idle_aap":true}}`)
	_, err = r.DynamicClient.Resource(apiInfo.aapGVR).Namespace(pod.Namespace).Patch(ctx, aap.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", err
	}

	log.FromContext(ctx).Info("AAP idled", "name", aap.GetName())

	return aap.GetName(), nil
}

// getAAPOwner returns the top level owner of the given object if it is an AAP instance.
func (r *Reconciler) getAAPOwner(ctx context.Context, obj metav1.Object, aapAPI apiInfo) (metav1.Object, error) {
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
	gvr, err := gvrForKind(owner.Kind, owner.APIVersion, aapAPI)
	if err != nil {
		return nil, err
	}
	// Get the owner object
	ownerObject, err := r.DynamicClient.Resource(*gvr).Namespace(obj.GetNamespace()).Get(ctx, owner.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
		logger.Info("Owner not found", "kind", owner.Kind, "name", owner.Name)
		return nil, nil
	}
	if ownerObject.GetKind() == aapKind {
		// Found the top AAP owner. Return it.
		return ownerObject, nil
	}

	// Recursively try to find the top AAP owner
	return r.getAAPOwner(ctx, ownerObject, aapAPI)
}

// gvrForKind returns GVR for the kind, if it's found in the available API list in the cluster
// returns an error if not found or failed to parse the API version
func gvrForKind(kind, apiVersion string, apiInfo apiInfo) (*schema.GroupVersionResource, error) {
	gvr, err := findGVRForKind(kind, apiVersion, apiInfo.resourceLists)
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
