package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type APIClient struct {
	AllNamespacesClient runtimeclient.Client
	Client              runtimeclient.Client
	Scheme              *runtime.Scheme
	GetHostCluster      cluster.GetHostClusterFunc
	AvailableAPIGroups  []metav1.APIGroup
}

// ApplyToolchainObjects applies the given ToolchainObjects with the given labels.
// If any object is marked as optional, then it checks if the API group is available - if not, then it skips the object.
func (c APIClient) ApplyToolchainObjects(ctx context.Context, toolchainObjects []runtimeclient.Object, newLabels map[string]string) (bool, error) {
	anyApplied := false
	logger := log.FromContext(ctx)
	for _, o := range toolchainObjects {
		gvk, err := getGvk(o, c.Client.Scheme())
		if err != nil {
			return anyApplied, err
		}
		if _, exists := o.GetAnnotations()[toolchainv1alpha1.TierTemplateObjectOptionalResourceAnnotation]; exists {
			if !apiGroupIsPresent(c.AvailableAPIGroups, gvk) {
				logger.Info("the object is marked as optional and the API group is not present - skipping...", "gvk", o.GetObjectKind().GroupVersionKind().String(), "name", o.GetName())
				continue
			}
		}
		// we could theoretically work on the "o" itself, but let's not change the contents of the incoming parameters
		toPatch := o.DeepCopyObject().(runtimeclient.Object)

		// SSA requires the GVK to be set on the object (which is not the case usually) and the managedFields to be nil
		toPatch.SetManagedFields(nil)
		toPatch.GetObjectKind().SetGroupVersionKind(gvk)

		toPatch.SetLabels(mergeLabels(toPatch.GetLabels(), newLabels))

		if err := c.Client.Patch(ctx, toPatch, runtimeclient.Apply, runtimeclient.FieldOwner("kubesaw"), runtimeclient.ForceOwnership); err != nil {
			return anyApplied, fmt.Errorf("unable to patch '%s' called '%s' in namespace '%s': %w", gvk, o.GetName(), o.GetNamespace(), err)
		}

		anyApplied = true
	}
	return anyApplied, nil
}

func mergeLabels(a, b map[string]string) map[string]string {
	if a == nil {
		return b
	} else {
		for k, v := range b {
			a[k] = v
		}
		return a
	}
}

func getGvk(obj runtimeclient.Object, scheme *runtime.Scheme) (schema.GroupVersionKind, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Empty() {
		gvks, _, err := scheme.ObjectKinds(obj)
		if err != nil {
			return schema.GroupVersionKind{}, err
		}
		if len(gvks) != 1 {
			return schema.GroupVersionKind{}, fmt.Errorf("the scheme maps the object of type %T into more than 1 GVK. This is not supported at the moment", obj)
		}

		return gvks[0], nil
	}

	return gvk, nil
}

func apiGroupIsPresent(availableAPIGroups []metav1.APIGroup, gvk schema.GroupVersionKind) bool {
	for _, group := range availableAPIGroups {
		if group.Name == gvk.Group {
			for _, version := range group.Versions {
				if version.Version == gvk.Version {
					return true
				}
			}
		}
	}
	return false
}
