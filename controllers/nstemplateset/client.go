package nstemplateset

import (
	"context"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"k8s.io/apimachinery/pkg/api/errors"
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
	GetHostCluster      cluster.GetClustersFunc
	AvailableAPIGroups  []metav1.APIGroup
}

// ApplyToolchainObjects applies the given ToolchainObjects with the given labels.
// If any object is marked as optional, then it checks if the API group is available - if not, then it skips the object.
func (c APIClient) ApplyToolchainObjects(ctx context.Context, toolchainObjects []runtimeclient.Object, newLabels map[string]string) (bool, error) {
	applyClient := applycl.NewApplyClient(c.Client)
	anyApplied := false
	logger := log.FromContext(ctx)

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
			logger.Info("the object is a ServiceAccount so we do the special handling for it...", "object_namespace", object.GetNamespace(), "object_name", object.GetObjectKind().GroupVersionKind().Kind+"/"+object.GetName())
			sa := object.DeepCopyObject().(runtimeclient.Object)
			err := applyClient.Get(ctx, runtimeclient.ObjectKeyFromObject(object), sa)
			if err != nil && !errors.IsNotFound(err) {
				return anyApplied, err
			}
			if err != nil {
				logger.Info("the ServiceAccount does not exists - creating...")
				applycl.MergeLabels(object, newLabels)
				if err := applyClient.Create(ctx, object); err != nil {
					return anyApplied, err
				}
			} else {
				logger.Info("the ServiceAccount already exists - updating labels and annotations...")
				applycl.MergeLabels(sa, newLabels)                    // add new labels to existing one
				applycl.MergeLabels(sa, object.GetLabels())           // add new labels from template
				applycl.MergeAnnotations(sa, object.GetAnnotations()) // add new annotations from template
				err = applyClient.Update(ctx, sa)
				if err != nil {
					return anyApplied, err
				}
			}
			anyApplied = true
			continue
		}
		logger.Info("applying object", "object_namespace", object.GetNamespace(), "object_name", object.GetObjectKind().GroupVersionKind().Kind+"/"+object.GetName())
		_, err := applyClient.Apply(ctx, []runtimeclient.Object{object}, newLabels)
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
