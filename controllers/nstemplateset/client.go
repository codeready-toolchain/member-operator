package nstemplateset

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
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
func (c APIClient) ApplyToolchainObjects(logger logr.Logger, toolchainObjects []runtimeclient.Object, newLabels map[string]string) (bool, error) {
	applyClient := applycl.NewApplyClient(c.Client, c.Scheme)
	anyApplied := false

	for _, object := range toolchainObjects {
		if _, exists := object.GetAnnotations()[toolchainv1alpha1.TierTemplateObjectOptionalResourceAnnotation]; exists {
			if !apiGroupIsPresent(c.AvailableAPIGroups, object.GetObjectKind().GroupVersionKind()) {
				logger.Info("the object is marked as optional and the API group is not present - skipping...", "gvk", object.GetObjectKind().GroupVersionKind().String(), "name", object.GetName())
				continue
			}
		}
		logger.Info("applying object", "object_namespace", object.GetNamespace(), "object_name", object.GetObjectKind().GroupVersionKind().Kind+"/"+object.GetName())
		_, err := applyClient.Apply([]runtimeclient.Object{object}, newLabels)
		if err != nil {
			return anyApplied, err
		}
		anyApplied = true
	}

	return anyApplied, nil
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
