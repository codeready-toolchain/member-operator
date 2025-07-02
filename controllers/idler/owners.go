package idler

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/scale"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ownerIdler struct {
	ownerFetcher  *ownerFetcher
	dynamicClient dynamic.Interface
	scalesClient  scale.ScalesGetter
	restClient    rest.Interface
}

func newOwnerIdler(discoveryClient discovery.ServerResourcesInterface, dynamicClient dynamic.Interface, scalesClient scale.ScalesGetter, restClient rest.Interface) *ownerIdler {
	return &ownerIdler{
		ownerFetcher:  newOwnerFetcher(discoveryClient, dynamicClient),
		dynamicClient: dynamicClient,
		scalesClient:  scalesClient,
		restClient:    restClient,
	}
}

// scaleOwnerToZero fetches the whole tree of the controller owners from the provided object (Deployment, ReplicaSet, etc).
// If any known controller owner is found, then it's scaled down (or deleted) and its kind and name is returned.
// Otherwise, returns empty strings.
func (i *ownerIdler) scaleOwnerToZero(ctx context.Context, meta metav1.Object) (kind string, name string, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Scaling owner to zero", "name", meta.GetName())

	owners, err := i.ownerFetcher.getOwners(ctx, meta)
	if err != nil {
		logger.Error(err, "failed to find all owners, try to idle the workload with information that is available")
	}
	for _, ownerWithGVR := range owners {
		owner := ownerWithGVR.object
		kind = owner.GetObjectKind().GroupVersionKind().Kind
		name = owner.GetName()
		switch kind {
		case "Deployment", "ReplicaSet", "Integration", "KameletBinding", "StatefulSet", "ReplicationController":
			err = i.scaleToZero(ctx, ownerWithGVR)
			return
		case "DaemonSet", "Job":
			err = i.deleteResource(ctx, ownerWithGVR) // Nothing to scale down. Delete instead.
			return
		case "DeploymentConfig":
			err = i.scaleDeploymentConfigToZero(ctx, ownerWithGVR)
			return
		case "VirtualMachine":
			err = i.stopVirtualMachine(ctx, ownerWithGVR) // Nothing to scale down. Stop instead.
			return
		case "AnsibleAutomationPlatform":
			err = i.idleAAP(ctx, ownerWithGVR) // Nothing to scale down. Stop instead.
			return
		case "Notebook":
			err = i.stopNotebook(ctx, ownerWithGVR) // Nothing to scale down. Stop instead.
			return
		}
	}
	return "", "", nil
}

var supportedScaleResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithKind("Integration"):          schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithResource("integrations"),
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithKind("KameletBinding"): schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithResource("kameletbindings"),
}

func (i *ownerIdler) scaleToZero(ctx context.Context, objectWithGVR *objectWithGVR) error {
	object := objectWithGVR.object
	logger := log.FromContext(ctx).WithValues("kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	logger.Info("Scaling controller owner to zero")

	patch := []byte(`{"spec":{"replicas":0}}`)
	for _, groupVersionResource := range supportedScaleResources {
		if groupVersionResource.String() == objectWithGVR.gvr.String() {
			logger.Info("Scaling controller owner to zero using the scale subresource")
			_, err := i.scalesClient.Scales(object.GetNamespace()).Patch(ctx, *objectWithGVR.gvr, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
			if err != nil {
				return err
			}
			logger.Info("Controller owner scaled to zero using the scale subresource")
			return nil
		}
	}

	_, err := i.dynamicClient.
		Resource(*objectWithGVR.gvr).
		Namespace(object.GetNamespace()).
		Patch(ctx, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	logger.Info("Controller owner scaled to zero")
	return nil
}

// idleAAP idles AAP instance if not already idled
func (i *ownerIdler) idleAAP(ctx context.Context, objectWithGVR *objectWithGVR) error {
	aapName := objectWithGVR.object.GetName()
	logger := log.FromContext(ctx).WithValues("name", aapName)
	idled, _, err := unstructured.NestedBool(objectWithGVR.object.UnstructuredContent(), "spec", "idle_aap")
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
		Resource(*objectWithGVR.gvr).
		Namespace(objectWithGVR.object.GetNamespace()).
		Patch(ctx, aapName, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}

	logger.Info("AAP idled", "name", aapName)
	return nil
}

func (i *ownerIdler) deleteResource(ctx context.Context, objectWithGVR *objectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.object
	logger.Info("Deleting controller owner",
		"kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	// see https://github.com/kubernetes/kubernetes/issues/20902#issuecomment-321484735
	// also, this may be needed for the e2e tests if the call to `client.Delete` comes too quickly after creating the job,
	// which may leave the job's pod running but orphan, hence causing a test failure (and making the test flaky)
	propagationPolicy := metav1.DeletePropagationBackground

	err := i.dynamicClient.
		Resource(*objectWithGVR.gvr).
		Namespace(object.GetNamespace()).
		Delete(ctx, object.GetName(), metav1.DeleteOptions{PropagationPolicy: &propagationPolicy})
	if err != nil {
		return err
	}

	logger.Info("Controller owner deleted",
		"kind", object.GetObjectKind().GroupVersionKind().Kind, "name", object.GetName())
	return nil
}

func (i *ownerIdler) scaleDeploymentConfigToZero(ctx context.Context, objectWithGVR *objectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.object
	logger.Info("Scaling DeploymentConfig to zero", "name", object.GetName())
	patch := []byte(`{"spec":{"replicas":0,"paused":false}}`)
	_, err := i.dynamicClient.
		Resource(*objectWithGVR.gvr).
		Namespace(object.GetNamespace()).
		Patch(ctx, object.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	log.FromContext(ctx).Info("DeploymentConfig scaled to zero", "name", object.GetName())
	return nil
}

func (i *ownerIdler) stopVirtualMachine(ctx context.Context, objectWithGVR *objectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.object
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

func (i *ownerIdler) stopNotebook(ctx context.Context, objectWithGVR *objectWithGVR) error {
	logger := log.FromContext(ctx)
	object := objectWithGVR.object
	logger.Info("Stopping Notebook", "name", object.GetName())

	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Set the kubeflow-resource-stopped annotation with current timestamp
	annotations["kubeflow-resource-stopped"] = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	object.SetAnnotations(annotations)

	_, err := i.dynamicClient.
		Resource(*objectWithGVR.gvr).
		Namespace(object.GetNamespace()).
		Update(ctx, object, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	logger.Info("Notebook stopped", "name", object.GetName())
	return nil
}

type ownerFetcher struct {
	resourceLists   []*metav1.APIResourceList // All available API in the cluster
	discoveryClient discovery.ServerResourcesInterface
	dynamicClient   dynamic.Interface
}

func newOwnerFetcher(discoveryClient discovery.ServerResourcesInterface, dynamicClient dynamic.Interface) *ownerFetcher {
	return &ownerFetcher{
		discoveryClient: discoveryClient,
		dynamicClient:   dynamicClient,
	}
}

type objectWithGVR struct {
	object *unstructured.Unstructured
	gvr    *schema.GroupVersionResource
}

// getOwners returns the whole tree of all controller owners going recursively to the top owner for the given object
func (o *ownerFetcher) getOwners(ctx context.Context, obj metav1.Object) ([]*objectWithGVR, error) {
	if o.resourceLists == nil {
		// Get all API resources from the cluster using the discovery client. We need it for constructing GVRs for unstructured objects.
		// Do it here once, so we do not have to list it multiple times before listing/getting every unstructured resource.
		resourceLists, err := o.discoveryClient.ServerPreferredResources()
		if err != nil {
			return nil, err
		}
		o.resourceLists = resourceLists
	}

	// get the controller owner (it's possible to have only one controller owner)
	owners := obj.GetOwnerReferences()
	var ownerReference metav1.OwnerReference
	var nonControllerOwner metav1.OwnerReference
	for _, ownerRef := range owners {
		// try to get the controller owner as the preferred one
		if ownerRef.Controller != nil && *ownerRef.Controller {
			ownerReference = ownerRef
			break
		} else if nonControllerOwner.Name == "" {
			// take only the first non-controller owner
			nonControllerOwner = ownerRef
		}
	}
	// if no controller owner was found, then use the first non-controller owner (if present)
	if ownerReference.Name == "" {
		ownerReference = nonControllerOwner
	}
	if ownerReference.Name == "" {
		return nil, nil // No owner
	}
	// Get the GVR for the owner
	gvr, err := gvrForKind(ownerReference.Kind, ownerReference.APIVersion, o.resourceLists)
	if err != nil {
		return nil, err
	}
	// Get the owner object
	ownerObject, err := o.dynamicClient.Resource(*gvr).Namespace(obj.GetNamespace()).Get(ctx, ownerReference.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	owner := &objectWithGVR{
		object: ownerObject,
		gvr:    gvr,
	}
	// Recursively try to find the top owner
	ownerOwners, err := o.getOwners(ctx, ownerObject)
	if err != nil || owners == nil {
		return append(ownerOwners, owner), err
	}
	return append(ownerOwners, owner), nil
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
