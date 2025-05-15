package idler

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	object client.Object
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
	for _, ownerRef := range owners {
		if ownerRef.Controller != nil && *ownerRef.Controller {
			ownerReference = ownerRef
			break
		}
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
		return []*objectWithGVR{owner}, err
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
