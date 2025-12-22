package idler

import (
	"context"
	"errors"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/owners"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/scale"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ownerIdler struct {
	idler         *toolchainv1alpha1.Idler
	ownerFetcher  *owners.OwnerFetcher
	dynamicClient dynamic.Interface
	scalesClient  scale.ScalesGetter
	restClient    rest.Interface
}

func newOwnerIdler(idler *toolchainv1alpha1.Idler, reconciler *Reconciler) *ownerIdler {
	return &ownerIdler{
		idler:         idler,
		ownerFetcher:  owners.NewOwnerFetcher(reconciler.DiscoveryClient, reconciler.DynamicClient),
		dynamicClient: reconciler.DynamicClient,
		scalesClient:  reconciler.ScalesClient,
		restClient:    reconciler.RestClient,
	}
}

// scaleOwnerToZero fetches the whole tree of the controller owners from the provided pod.
// If any known controller owner is found, then it's scaled down (or deleted) and its kind and name is returned.
// If the pod has been running for longer than 105% of the idler timeout, it will also idle the second known owner.
// This is a workaround for cases when the top owner controller fails to idle the workload. For example the AAP controller sometimes fails to scale down StatefulSets for postgres pods owned by the top AAP CR. Scaling down the StatefulSet (AAP -> StatefulSet -> Pods) mitigates that AAP controller bug.
// Otherwise, returns empty strings.
func (i *ownerIdler) scaleOwnerToZero(ctx context.Context, pod *corev1.Pod) (string, string, error) {
	logger := log.FromContext(ctx)
	logger.Info("Scaling owner to zero")

	owners, err := i.ownerFetcher.GetOwners(ctx, pod)
	if err != nil {
		logger.Error(err, "failed to find all owners, try to idle the workload with information that is available")
	}

	var topOwnerKind, topOwnerName string
	var errToReturn error
	for _, ownerWithGVR := range owners {
		owner := ownerWithGVR.Object
		ownerKind := owner.GetObjectKind().GroupVersionKind().Kind

		switch ownerKind {
		case "Deployment", "ReplicaSet", "Integration", "KameletBinding", "StatefulSet", "ReplicationController":
			err = i.scaleToZero(ctx, ownerWithGVR)
		case "DaemonSet", "Job", "DataVolume":
			err = i.deleteResource(ctx, ownerWithGVR) // Nothing to scale down. Delete instead.
		case "DeploymentConfig":
			err = i.scaleDeploymentConfigToZero(ctx, ownerWithGVR)
		case "VirtualMachine":
			err = i.stopVirtualMachine(ctx, ownerWithGVR) // Nothing to scale down. Stop instead.
		case "AnsibleAutomationPlatform":
			err = i.idleAAP(ctx, ownerWithGVR) // Nothing to scale down. Stop instead.
		case "ServingRuntime":
			err = i.idleServingRuntime(ctx, ownerWithGVR) // Idle by deleting old InferenceService objects.
		default:
			continue // Skip unknown owner types
		}

		// Store the first processed owner's info and preserve its error
		if topOwnerKind == "" {
			topOwnerKind = ownerKind
			topOwnerName = owner.GetName()
			errToReturn = err
		} else {
			// if it's the second known owner being processed, then stop the loop
			errToReturn = errors.Join(errToReturn, err)
			break
		}

		// If no error occurred and the pod doesn't run for longer than 105% of the idler timeout, return immediately after the first owner was idled
		timeoutSeconds := getTimeout(i.idler, *pod)
		if err == nil && !time.Now().After(pod.Status.StartTime.Add(time.Duration(float64(timeoutSeconds)*1.05)*time.Second)) {
			return topOwnerKind, topOwnerName, nil
		}
		logger.Info("Scaling the first known owner down either failed or the pod has been running for longer than 105% of the idler timeout. Scaling the next known owner.")
	}

	// Return the first processed owner's info (or empty if none were processed), and the list of errors (if any happened)
	return topOwnerKind, topOwnerName, errToReturn
}

var supportedScaleResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithKind("Integration"):          schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithResource("integrations"),
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithKind("KameletBinding"): schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithResource("kameletbindings"),
}

func (i *ownerIdler) scaleToZero(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	object := objectWithGVR.Object
	logger := log.FromContext(ctx).WithValues("kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	logger.Info("Scaling controller owner to zero")

	patch := []byte(`{"spec":{"replicas":0}}`)
	for _, groupVersionResource := range supportedScaleResources {
		if groupVersionResource.String() == objectWithGVR.GVR.String() {
			logger.Info("Scaling controller owner to zero using the scale subresource")
			_, err := i.scalesClient.Scales(object.GetNamespace()).Patch(ctx, *objectWithGVR.GVR, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
			if err != nil {
				return err
			}
			logger.Info("Controller owner scaled to zero using the scale subresource")
			return nil
		}
	}

	_, err := i.dynamicClient.
		Resource(*objectWithGVR.GVR).
		Namespace(object.GetNamespace()).
		Patch(ctx, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	logger.Info("Controller owner scaled to zero")
	return nil
}

// idleAAP idles AAP instance if not already idled
func (i *ownerIdler) idleAAP(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	aapName := objectWithGVR.Object.GetName()
	logger := log.FromContext(ctx).WithValues("name", aapName)
	idled, _, err := unstructured.NestedBool(objectWithGVR.Object.UnstructuredContent(), "spec", "idle_aap")
	if err != nil {
		logger.Error(err, "Failed to parse AAP CR to get the spec.idle_aap field")
	}
	if idled {
		logger.Info("AAP CR is already idled")
		return nil
	}
	logger.Info("Idling AAP")

	// Patch the aap resource by setting spec.idle_aap to true in order to idle it
	patch := []byte(`{"spec":{"idle_aap":true}}`)
	_, err = i.dynamicClient.
		Resource(*objectWithGVR.GVR).
		Namespace(objectWithGVR.Object.GetNamespace()).
		Patch(ctx, aapName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	logger.Info("AAP idled", "name", aapName)
	return nil
}

func (i *ownerIdler) deleteResource(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.Object
	logger.Info("Deleting controller owner",
		"kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	// see https://github.com/kubernetes/kubernetes/issues/20902#issuecomment-321484735
	// also, this may be needed for the e2e tests if the call to `client.Delete` comes too quickly after creating the job,
	// which may leave the job's pod running but orphan, hence causing a test failure (and making the test flaky)
	propagationPolicy := metav1.DeletePropagationBackground

	err := i.dynamicClient.
		Resource(*objectWithGVR.GVR).
		Namespace(object.GetNamespace()).
		Delete(ctx, object.GetName(), metav1.DeleteOptions{PropagationPolicy: &propagationPolicy})
	if err != nil {
		return err
	}

	logger.Info("Controller owner deleted",
		"kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	return nil
}

func (i *ownerIdler) scaleDeploymentConfigToZero(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.Object
	logger.Info("Scaling DeploymentConfig to zero", "name", object.GetName())
	patch := []byte(`{"spec":{"replicas":0,"paused":false}}`)
	_, err := i.dynamicClient.
		Resource(*objectWithGVR.GVR).
		Namespace(object.GetNamespace()).
		Patch(ctx, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	log.FromContext(ctx).Info("DeploymentConfig scaled to zero", "name", object.GetName())
	return nil
}

func (i *ownerIdler) stopVirtualMachine(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.Object
	logger.Info("Stopping VirtualMachine", "name", object.GetName())
	err := i.restClient.Put().
		AbsPath(fmt.Sprintf(vmSubresourceURLFmt, "v1")).
		Namespace(object.GetNamespace()).
		Resource("virtualmachines").
		Name(object.GetName()).
		SubResource("stop").
		Do(ctx).
		Error()
	if err != nil {
		return err
	}

	logger.Info("VirtualMachine stopped", "name", object.GetName())
	return nil
}

// idleServingRuntime idles ServingRuntime by deleting InferenceService objects that exist for longer than the timeout
func (i *ownerIdler) idleServingRuntime(ctx context.Context, objectWithGVR *owners.ObjectWithGVR) error {
	logger := log.FromContext(ctx)
	namespace := objectWithGVR.Object.GetNamespace()

	logger.Info("Idling ServingRuntime by deleting old InferenceService objects", "name", objectWithGVR.Object.GetName())

	// Construct GVR for InferenceService objects
	inferenceServiceGVR := schema.GroupVersionResource{
		Group:    "serving.kserve.io",
		Version:  "v1beta1",
		Resource: "inferenceservices",
	}

	// List all InferenceService objects in the namespace
	inferenceServiceList, err := i.dynamicClient.
		Resource(inferenceServiceGVR).
		Namespace(namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list InferenceService objects: %w", err)
	}

	cutoffTime := time.Now().Add(-time.Duration(i.idler.Spec.TimeoutSeconds) * time.Second)
	var deletionErrors []error

	// Delete InferenceService objects that are older than the timeout
	for _, inferenceService := range inferenceServiceList.Items {
		creationTime := inferenceService.GetCreationTimestamp().Time
		if creationTime.Before(cutoffTime) {
			logger.Info("Deleting old InferenceService", "name", inferenceService.GetName(), "age", time.Since(creationTime))

			err := i.dynamicClient.
				Resource(inferenceServiceGVR).
				Namespace(namespace).
				Delete(ctx, inferenceService.GetName(), metav1.DeleteOptions{})
			if err != nil {
				deletionErrors = append(deletionErrors, err)
			} else {
				logger.Info("InferenceService deleted", "name", inferenceService.GetName())
			}
		}
	}

	return errors.Join(deletionErrors...)
}
