package idler

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	aapKind       = "AnsibleAutomationPlatform"
	aapAPIVersion = "aap.ansible.com/v1alpha1"
)

type aapAPI struct {
	GVR           schema.GroupVersionResource // AAP GVR
	ResourceLists []*metav1.APIResourceList   // All available API in the cluster
}

// ensureAnsiblePlatformIdling checks if there is any long-running pod belonging to an AAP resource and if yes, then it idles the AAP
// and sends a notification to the user.
func (r *Reconciler) ensureAnsiblePlatformIdling(ctx context.Context, idler *toolchainv1alpha1.Idler) error {

	// Get all API resources from the cluster using the discovery client. We need it for constructing GVRs for unstructured objects.
	// Do it here once, so we do not have to list it multiple times before listing/getting every unstructured resource.
	resourceLists, err := r.DiscoveryClient.ServerPreferredResources()
	if err != nil {
		return err
	}

	// Check if the AAP API is even available/installed
	aapGVR, err := findGVRForKind(aapKind, aapAPIVersion, resourceLists)
	if err != nil {
		return err
	}
	if aapGVR == nil {
		// AAP API is not available/installed. Skipping.
		return nil
	}
	aapAPI := aapAPI{
		ResourceLists: resourceLists,
		GVR:           *aapGVR,
	}

	// Check if there is any AAP CRs in the namespace
	idledAAPs := []string{}
	aapList, err := r.DynamicClient.Resource(aapAPI.GVR).Namespace(idler.Name).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(aapList.Items) == 0 {
		// No AAP resources found. Nothing to idle.
		return nil
	}

	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.AllNamespacesClient.List(ctx, podList, client.InNamespace(idler.Name)); err != nil {
		return err
	}
	for _, pod := range podList.Items {
		var aapName string
		pod := pod // TODO We won't need it after upgrading to go 1.22: https://go.dev/blog/loopvar-preview
		podLogger := log.FromContext(ctx).WithValues("pod_name", pod.Name, "pod_phase", pod.Status.Phase)
		podCtx := log.IntoContext(ctx, podLogger)

		// check the restart count for the pod
		restartCount := getHighestRestartCount(pod.Status)
		if restartCount > AAPRestartThreshold {
			podLogger.Info("Pod is restarting too often for an AAP pod. Checking if it belongs to AAP and if so then idle the aap", "restart_count", restartCount)
			aapName, err = r.ensureAAPIdled(podCtx, pod, aapAPI)
			if err != nil {
				return err
			}
		} else {
			// Check if running for longer then the AAP idler timeout
			timeoutSeconds := r.aapTimeoutSeconds(idler)
			if pod.Status.StartTime != nil && time.Now().After(pod.Status.StartTime.Add(time.Duration(timeoutSeconds)*time.Second)) {
				podLogger.Info("Pod is running for too long for an AAP pod. Checking if it belongs to AAP and if so then idle the aap.", "start_time", pod.Status.StartTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", timeoutSeconds)
				aapName, err = r.ensureAAPIdled(podCtx, pod, aapAPI)
				if err != nil {
					return err
				}
			}
		}

		// Check if we need to send a notification and proceed with the next pod
		if aapName != "" {
			// The AAP has been Idled
			// A notification should be sent
			if err := r.createNotification(podCtx, idler, aapName, "Ansible Automation Platform"); err != nil {
				podLogger.Error(err, "failed to create Notification")
				if err = r.setStatusIdlerNotificationCreationFailed(podCtx, idler, err.Error()); err != nil {
					podLogger.Error(err, "failed to set status IdlerNotificationCreationFailed")
				} // not returning error to continue tracking remaining pods
			}

			idledAAPs = append(idledAAPs, aapName)
			if checkIfAllAAPsIdled(aapList, idledAAPs) {
				// All AAPs are idled, no need to check the rest of the pods
				return nil
			}
		}
	}

	return nil
}

func checkIfAllAAPsIdled(allAAPs *unstructured.UnstructuredList, idledAAPs []string) bool {
	if len(allAAPs.Items) != len(idledAAPs) {
		return false
	}
	for _, aap := range allAAPs.Items {
		found := false
		for _, idledAAP := range idledAAPs {
			if aap.GetName() == idledAAP {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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
func (r *Reconciler) ensureAAPIdled(ctx context.Context, pod corev1.Pod, aapAPI aapAPI) (string, error) {
	podCondition := pod.Status.Conditions
	for _, podCond := range podCondition {
		if podCond.Type == "PodCompleted" {
			// Pod is in the completed state, no need to idle
			return "", nil
		}
	}

	aap, err := r.getAAPOwner(ctx, &pod, aapAPI)
	if err != nil {
		return "", err
	}
	if aap == nil {
		return "", nil // No AAP owner found
	}

	// Patch the aap resource by setting spec.idle_aap to true in order to idle it
	patch := []byte(`{"spec":{"idle_aap":true}}`)
	_, err = r.DynamicClient.Resource(aapAPI.GVR).Namespace(pod.Namespace).Patch(ctx, aap.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", err
	}

	log.FromContext(ctx).Info("AAP idled", "name", aap.GetName())

	return aap.GetName(), nil
}

// getAAPOwner returns the top level owner of the given object if it is an AAP instance.
func (r *Reconciler) getAAPOwner(ctx context.Context, obj metav1.Object, aapAPI aapAPI) (metav1.Object, error) {
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
		return obj, nil
	}

	// Recursively try to find the top AAP owner
	return r.getAAPOwner(ctx, ownerObject, aapAPI)
}

// gvrForKind returns GVR for the kind, if it's found in the available API list in the cluster
// returns an error if not found or failed to parse the API version
func gvrForKind(kind, apiVersion string, aapAPI aapAPI) (*schema.GroupVersionResource, error) {
	gvr, err := findGVRForKind(kind, apiVersion, aapAPI.ResourceLists)
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
