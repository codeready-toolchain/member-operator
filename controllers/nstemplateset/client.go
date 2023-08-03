package nstemplateset

import (
	"context"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	applyClient := applycl.NewApplyClient(c.Client)
	anyApplied := false

	for _, object := range toolchainObjects {
		if _, exists := object.GetAnnotations()[toolchainv1alpha1.TierTemplateObjectOptionalResourceAnnotation]; exists {
			if !apiGroupIsPresent(c.AvailableAPIGroups, object.GetObjectKind().GroupVersionKind()) {
				logger.Info("the object is marked as optional and the API group is not present - skipping...", "gvk", object.GetObjectKind().GroupVersionKind().String(), "name", object.GetName())
				continue
			}
		}
		// Special handling of ServiceAccounts is required because if a ServiceAccount is reapplied when it already exists, it causes Kubernetes controllers to
		// automatically create new Secrets for the ServiceAccounts. After enough time the number of Secrets created will hit the Secrets quota and then no new
		// Secrets can be created. To prevent this from happening, we fetch the already existing SA, update labels and annotations only, and then call update using the same object (keeping the refs to secrets).
		if strings.EqualFold(object.GetObjectKind().GroupVersionKind().Kind, "ServiceAccount") {
			sa := &v1.ServiceAccount{}
			err := applyClient.Get(context.TODO(), runtimeclient.ObjectKeyFromObject(object), sa)
			if err != nil && !errors.IsNotFound(err) {
				return anyApplied, err
			}
			if errors.IsNotFound(err) {
				logger.Info("the object is a service account but was not found", "object_namespace", object.GetNamespace(), "object_name", object.GetObjectKind().GroupVersionKind().Kind+"/"+object.GetName())
			}
			// update labels and annotations for service account
			if err == nil {
				logger.Info("the object is a ServiceAccount and already exists - updating labels and annotations...")
				applycl.MergeLabels(sa, newLabels)                    // add new labels to existing one
				applycl.MergeLabels(sa, object.GetLabels())           // add new labels from template
				applycl.MergeAnnotations(sa, object.GetAnnotations()) // add new annotations from template
				err = applyClient.Update(context.TODO(), sa)
				if err != nil {
					return anyApplied, err
				}
				anyApplied = true
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
