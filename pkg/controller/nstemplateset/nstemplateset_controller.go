package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/pkg/controller"
	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_nstemplateset")

// Add creates a new NSTemplateSetReconciler and starts it (ie, watches resources and reconciles the cluster state)
func Add(mgr manager.Manager, _ *configuration.Config) error {
	return add(mgr, newReconciler(&apiClient{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		getHostCluster: cluster.GetHostCluster,
	}))
}

func newReconciler(apiClient *apiClient) *NSTemplateSetReconciler {
	status := &statusManager{
		apiClient: apiClient,
	}
	return &NSTemplateSetReconciler{
		apiClient: apiClient,
		status:    status,
		namespaces: &namespacesManager{
			statusManager: status,
		},
		clusterResources: &clusterResourcesManager{
			statusManager: status,
		},
	}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("nstemplateset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resources: NSTemplateSets
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resources: Namespaces associated with an NSTemplateSet (not owned, though - see https://issues.redhat.com/browse/CRT-429)
	if err := c.Watch(&source.Kind{Type: &corev1.Namespace{}}, commoncontroller.MapToOwnerByLabel("", toolchainv1alpha1.OwnerLabelKey)); err != nil {
		return err
	}
	// also, watch for secondary resources: cluster resources quotas associated with an NSTemplateSet, too
	if err := c.Watch(&source.Kind{Type: &quotav1.ClusterResourceQuota{}}, commoncontroller.MapToOwnerByLabel("", toolchainv1alpha1.OwnerLabelKey)); err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &NSTemplateSetReconciler{}

type apiClient struct {
	client         client.Client
	scheme         *runtime.Scheme
	getHostCluster cluster.GetHostClusterFunc
}

// NSTemplateSetReconciler the NSTemplateSet reconciler
type NSTemplateSetReconciler struct {
	*apiClient
	namespaces       *namespacesManager
	clusterResources *clusterResourcesManager
	status           *statusManager
}

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *NSTemplateSetReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("reconciling NSTemplateSet")

	var err error
	namespace, err := getNamespaceName(request)
	if err != nil {
		logger.Error(err, "failed to determine resource namespace")
		return reconcile.Result{}, err
	}

	// Fetch the NSTemplateSet instance
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: request.Name}, nsTmplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to get NSTemplateSet")
		return reconcile.Result{}, err
	}
	if util.IsBeingDeleted(nsTmplSet) {
		return r.deleteNSTemplateSet(logger, nsTmplSet)
	}
	// make sure there's a finalizer
	if err := r.addFinalizer(nsTmplSet); err != nil {
		return reconcile.Result{}, err
	}

	// we proceed with the cluster-scoped resources template before all namespaces
	// as we want ot be sure that cluster scoped resources such as quotas are set
	// even before the namespaces exist
	if createdOrUpdated, err := r.clusterResources.ensure(logger, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		return reconcile.Result{}, nil // wait for cluster resources to be created
	}

	createdOrUpdated, err := r.namespaces.ensure(logger, nsTmplSet)
	if err != nil {
		logger.Error(err, "failed to either provision or update user namespaces")
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		return reconcile.Result{}, nil // something in the watched resources has changed - wait for another reconcile
	}

	return reconcile.Result{}, r.status.setStatusReady(nsTmplSet)
}

// addFinalizer sets the finalizers for NSTemplateSet
func (r *NSTemplateSetReconciler) addFinalizer(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		util.AddFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
		if err := r.client.Update(context.TODO(), nsTmplSet); err != nil {
			return err
		}
	}
	return nil
}

func (r *NSTemplateSetReconciler) deleteNSTemplateSet(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	// if the NSTemplateSet has no finalizer, then we don't have anything to do
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		logger.Info("NSTemplateSet resource is terminated")
		return reconcile.Result{}, nil
	}
	// since the NSTmplSet resource is being deleted, we must set its status to `ready=false/reason=terminating`
	if err := r.status.setStatusTerminating(nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to set status to 'ready=false/reason=terminating' on NSTemplateSet")
	}
	username := nsTmplSet.GetName()

	// delete all namespace one by one
	deletedAny, err := r.namespaces.delete(logger, nsTmplSet)
	if err != nil || deletedAny {
		return reconcile.Result{}, nil
	}

	// if no namespace was to be deleted, then we can proceed with the cluster resources associated with the user
	deletedAny, err = r.clusterResources.delete(logger, nsTmplSet)
	if err != nil || deletedAny {
		return reconcile.Result{}, nil
	}

	// if nothing was to be deleted, then we can remove the finalizer and we're done
	logger.Info("NSTemplateSet resource is ready to be terminated: all related user namespaces have been marked for deletion")
	util.RemoveFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
	if err := r.client.Update(context.TODO(), nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to remove finalizer on NSTemplateSet '%s'", username)
	}
	return reconcile.Result{}, nil
}

// deleteRedundantObjects takes template objects of the current tier and of the new tier (provided as newObjects param),
// compares their names and GVKs and deletes those ones that are in the current template but are not found in the new one.
// return `true, nil` if an object was deleted, `false, nil`/`false, err` otherwise
func deleteRedundantObjects(logger logr.Logger, client client.Client, deleteOnlyOne bool, currentObjs []runtime.RawExtension, newObjects []runtime.RawExtension) (bool, error) {
	deleted := false
	logger.Info("checking redundant objects", "count", len(currentObjs))
Current:
	for _, currentObj := range currentObjs {
		current, err := meta.Accessor(currentObj.Object)
		if err != nil {
			return false, err
		}
		logger.Info("checking redundant object", "objectName", currentObj.Object.GetObjectKind().GroupVersionKind().Kind+"/"+current.GetName())
		for _, newObj := range newObjects {
			newOb, err := meta.Accessor(newObj.Object)
			if err != nil {
				return false, err
			}
			if current.GetName() == newOb.GetName() &&
				currentObj.Object.GetObjectKind().GroupVersionKind() == newObj.Object.GetObjectKind().GroupVersionKind() {
				continue Current
			}
		}
		if err := client.Delete(context.TODO(), currentObj.Object); err != nil && !errors.IsNotFound(err) { // ignore if the object was already deleted
			return false, errs.Wrapf(err, "failed to delete object '%s' of kind '%s' in namespace '%s'", current.GetName(), currentObj.Object.GetObjectKind().GroupVersionKind().Kind, current.GetNamespace())
		} else if errors.IsNotFound(err) {
			continue // continue to the next object since this one was already deleted
		}
		logger.Info("deleted redundant object", "objectName", currentObj.Object.GetObjectKind().GroupVersionKind().Kind+"/"+current.GetName())
		if deleteOnlyOne {
			return true, nil
		}
		deleted = true
	}
	return deleted, nil
}

// listByOwnerLabel returns client.ListOption that filters by label toolchain.dev.openshift.com/owner equal to the given username
func listByOwnerLabel(username string) client.ListOption {
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: username}

	return client.MatchingLabels(labels)
}
