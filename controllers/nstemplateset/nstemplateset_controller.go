package nstemplateset

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/controllers"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	commonpredicates "github.com/codeready-toolchain/toolchain-common/pkg/predicate"
	"github.com/go-logr/logr"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func NewReconciler(apiClient *APIClient) *Reconciler {
	status := &statusManager{
		APIClient: apiClient,
	}
	return &Reconciler{
		APIClient: apiClient,
		status:    status,
		namespaces: &namespacesManager{
			statusManager: status,
		},
		clusterResources: &clusterResourcesManager{
			statusManager: status,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	mapToOwnerByLabel := handler.EnqueueRequestsFromMapFunc(commoncontroller.MapToOwnerByLabel("", toolchainv1alpha1.OwnerLabelKey))

	build := ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.NSTemplateSet{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, mapToOwnerByLabel)

	// watch for all cluster resource kinds associated with an NSTemplateSet
	for _, clusterResource := range clusterResourceKinds {
		// only reconcile generation changes for cluster resources
		build = build.Watches(&source.Kind{Type: clusterResource.objectType}, mapToOwnerByLabel, builder.WithPredicates(commonpredicates.LabelsAndGenerationPredicate{}))
	}

	return build.Complete(r)
}

type APIClient struct {
	Client         client.Client
	Scheme         *runtime.Scheme
	GetHostCluster cluster.GetHostClusterFunc
}

// Reconciler the NSTemplateSet reconciler
type Reconciler struct {
	*APIClient
	namespaces       *namespacesManager
	clusterResources *clusterResourcesManager
	status           *statusManager
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=namespaces;limitranges,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io;authorization.openshift.io,resources=rolebindings;roles;clusterroles;clusterrolebindings,verbs=*
//+kubebuilder:rbac:groups=quota.openshift.io,resources=clusterresourcequotas,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling NSTemplateSet")

	var err error
	namespace, err := getNamespaceName(request)
	if err != nil {
		logger.Error(err, "failed to determine resource namespace")
		return reconcile.Result{}, err
	}

	// Fetch the NSTemplateSet instance
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: request.Name}, nsTmplSet)
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
func (r *Reconciler) addFinalizer(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		util.AddFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(context.TODO(), nsTmplSet); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) deleteNSTemplateSet(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	// if the NSTemplateSet has no finalizer, then we don't have anything to do
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		logger.Info("NSTemplateSet resource is terminated")
		return reconcile.Result{}, nil
	}
	logger.Info("NSTemplateSet resource is being deleted")
	// since the NSTmplSet resource is being deleted, we must set its status to `ready=false/reason=terminating`
	if err := r.status.setStatusTerminating(nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to set status to 'ready=false/reason=terminating' on NSTemplateSet")
	}
	username := nsTmplSet.GetName()

	// delete all namespace one by one
	allDeleted, err := r.namespaces.ensureDeleted(logger, nsTmplSet)
	// when err, status Update will not trigger reconcile, sending returning error.
	if err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.status.setStatusTerminatingFailed, err, "failed to ensure namespace deletion")
	}
	if !allDeleted {
		if time.Since(nsTmplSet.DeletionTimestamp.Time) > 60*time.Second {
			return reconcile.Result{}, fmt.Errorf("NSTemplateSet deletion has not completed in over 1 minute")
		}
		// One or more namespaces may not yet be deleted. We can stop here.
		return reconcile.Result{
			Requeue:      true,
			RequeueAfter: time.Second,
		}, nil
	}

	// if no namespace was to be deleted, then we can proceed with the cluster resources associated with the user
	deletedAny, err := r.clusterResources.delete(logger, nsTmplSet)
	if err != nil || deletedAny {
		return reconcile.Result{}, nil
	}

	// if nothing was to be deleted, then we can remove the finalizer and we're done
	logger.Info("NSTemplateSet resource is ready to be terminated: all related user namespaces have been marked for deletion")
	util.RemoveFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
	if err := r.Client.Update(context.TODO(), nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to remove finalizer on NSTemplateSet '%s'", username)
	}
	return reconcile.Result{}, nil
}

// deleteRedundantObjects takes template objects of the current tier and of the new tier (provided as newObjects param),
// compares their names and GVKs and deletes those ones that are in the current template but are not found in the new one.
// return `true, nil` if an object was deleted, `false, nil`/`false, err` otherwise
func deleteRedundantObjects(logger logr.Logger, client client.Client, deleteOnlyOne bool, currentObjs []applycl.ToolchainObject, newObjects []applycl.ToolchainObject) (bool, error) {
	deleted := false
	logger.Info("checking redundant objects", "count", len(currentObjs))
Current:
	for _, currentObj := range currentObjs {
		logger.Info("checking redundant object", "objectName", currentObj.GetGvk().Kind+"/"+currentObj.GetName())
		for _, newObj := range newObjects {
			if currentObj.HasSameGvkAndName(newObj) {
				continue Current
			}
		}
		if err := client.Delete(context.TODO(), currentObj.GetClientObject()); err != nil && !errors.IsNotFound(err) { // ignore if the object was already deleted
			return false, errs.Wrapf(err, "failed to delete object '%s' of kind '%s' in namespace '%s'", currentObj.GetName(), currentObj.GetGvk().Kind, currentObj.GetNamespace())
		} else if errors.IsNotFound(err) {
			continue // continue to the next object since this one was already deleted
		}
		logger.Info("deleted redundant object", "objectName", currentObj.GetGvk().Kind+"/"+currentObj.GetName())
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

func isUpToDateAndProvisioned(obj metav1.Object, tierTemplate *tierTemplate) bool {
	return obj.GetLabels() != nil &&
		obj.GetLabels()[toolchainv1alpha1.TemplateRefLabelKey] == tierTemplate.templateRef &&
		obj.GetLabels()[toolchainv1alpha1.TierLabelKey] == tierTemplate.tierName
}
