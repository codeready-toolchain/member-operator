package nstemplateset

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/controllers"
	commonclient "github.com/codeready-toolchain/toolchain-common/pkg/client"
	commonpredicates "github.com/codeready-toolchain/toolchain-common/pkg/predicate"
	"k8s.io/client-go/discovery"

	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	runtimeCluster "sigs.k8s.io/controller-runtime/pkg/cluster"
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
		spaceRoles: &spaceRolesManager{
			statusManager: status,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager, allNamespaceCluster runtimeCluster.Cluster, discoveryClient *discovery.DiscoveryClient) error {
	apiGroupList, err := discoveryClient.ServerGroups()
	if err != nil {
		return err
	}

	mapToOwnerByLabel := handler.EnqueueRequestsFromMapFunc(commoncontroller.MapToOwnerByLabel("", toolchainv1alpha1.SpaceLabelKey))
	build := ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.NSTemplateSet{}, builder.WithPredicates(predicate.Or[runtimeclient.Object](predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&corev1.Namespace{}, mapToOwnerByLabel).
		// we're watching the roles and role bindings explicitly so that the users that accidentally lose access to their namespaces
		// can get it restored as quickly as possible.
		//
		// We watch no other resource kinds that are created from the templates. Instead we rely on the periodic reconcile of nstemplatesets that is automatically
		// triggered by the controller runtime roughly once a day.
		WatchesRawSource(source.Kind[runtimeclient.Object](allNamespaceCluster.GetCache(), &rbac.Role{}, mapToOwnerByLabel, commonpredicates.LabelsAndGenerationPredicate{})).
		WatchesRawSource(source.Kind[runtimeclient.Object](allNamespaceCluster.GetCache(), &rbac.RoleBinding{}, mapToOwnerByLabel, commonpredicates.LabelsAndGenerationPredicate{}))

	r.AllNamespacesClient = allNamespaceCluster.GetClient()
	r.AvailableAPIGroups = apiGroupList.Groups

	return build.Complete(r)
}

// Reconciler the NSTemplateSet reconciler
type Reconciler struct {
	*APIClient
	namespaces       *namespacesManager
	clusterResources *clusterResourcesManager
	spaceRoles       *spaceRolesManager
	status           *statusManager
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=nstemplatesets/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=namespaces;limitranges,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces;resourcequotas,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io;authorization.openshift.io,resources=rolebindings;roles;clusterroles;clusterrolebindings,verbs=*
//+kubebuilder:rbac:groups=quota.openshift.io,resources=clusterresourcequotas,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=environments,verbs=get;list;watch;create;update;delete

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
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: request.Name}, nsTmplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("NSTemplateSet not found")
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to get NSTemplateSet")
		return reconcile.Result{}, err
	}
	if util.IsBeingDeleted(nsTmplSet) {
		return r.deleteNSTemplateSet(ctx, nsTmplSet)
	}
	// make sure there's a finalizer
	if err := r.addFinalizer(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	}

	// we proceed with the cluster-scoped resources template, then all namespaces and finally space roles
	// as we want ot be sure that cluster scoped resources such as quotas are set
	// even before the namespaces exist
	if createdOrUpdated, err := r.clusterResources.ensure(ctx, nsTmplSet); err != nil {
		logger.Error(err, "failed to either provision or update cluster resources")
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		// we need to requeue to make sure we apply all cluster resources before continuing further
		return reconcile.Result{Requeue: true}, nil // wait for cluster resources to be created
	}
	if err := r.status.updateStatusClusterResourcesRevisions(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	}

	if createdOrUpdated, err := r.namespaces.ensure(ctx, nsTmplSet); err != nil {
		logger.Error(err, "failed to either provision or update user namespaces")
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		return reconcile.Result{}, nil
	}
	if err := r.status.updateStatusNamespacesRevisions(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	}

	// update provisioned namespace list
	if err := r.namespaces.setProvisionedNamespaceList(ctx, nsTmplSet); err != nil {
		logger.Error(err, "failed to set provisioned namespaces list")
		return reconcile.Result{}, err
	}

	if createdOrUpdated, err := r.spaceRoles.ensure(ctx, nsTmplSet); err != nil {
		logger.Error(err, "failed to either provision or update roles in space")
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		return reconcile.Result{}, nil
	}
	if err := r.status.updateStatusSpaceRolesRevisions(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, r.status.setStatusReady(ctx, nsTmplSet)
}

// addFinalizer sets the finalizers for NSTemplateSet
func (r *Reconciler) addFinalizer(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		util.AddFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(ctx, nsTmplSet); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) deleteNSTemplateSet(ctx context.Context, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	// if the NSTemplateSet has no finalizer, then we don't have anything to do
	if !util.HasFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName) {
		logger.Info("NSTemplateSet resource is terminated")
		return reconcile.Result{}, nil
	}
	logger.Info("NSTemplateSet resource is being deleted")
	// since the NSTmplSet resource is being deleted, we must set its status to `ready=false/reason=terminating`
	if err := r.status.setStatusTerminating(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to set status to 'ready=false/reason=terminating' on NSTemplateSet")
	}
	spacename := nsTmplSet.GetName()

	// delete all namespace one by one
	allDeleted, err := r.namespaces.ensureDeleted(ctx, nsTmplSet)
	// when err, status Update will not trigger reconcile, sending returning error.
	if err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.status.setStatusTerminatingFailed, err, "failed to ensure namespace deletion")
	}
	if !allDeleted {
		if time.Since(nsTmplSet.DeletionTimestamp.Time) > 60*time.Second {
			return reconcile.Result{}, fmt.Errorf("NSTemplateSet deletion has not completed in over 1 minute")
		}
		// One or more namespaces may not yet be deleted. We can stop here.
		return reconcile.Result{
			RequeueAfter: time.Second,
		}, nil
	}

	// if no namespace was to be deleted, then we can proceed with the cluster resources associated with the user
	deletedAny, err := r.clusterResources.delete(ctx, nsTmplSet)
	if err != nil || deletedAny {
		// we need to check if there are some more cluster resources left
		return reconcile.Result{Requeue: true}, err
	}

	// if nothing was to be deleted, then we can remove the finalizer and we're done
	logger.Info("NSTemplateSet resource is ready to be terminated: all related user namespaces have been marked for deletion")
	util.RemoveFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
	if err := r.Client.Update(ctx, nsTmplSet); err != nil {
		return reconcile.Result{}, r.status.wrapErrorWithStatusUpdate(ctx, nsTmplSet, r.status.setStatusTerminatingFailed, err,
			"failed to remove finalizer on NSTemplateSet '%s'", spacename)
	}
	return reconcile.Result{}, nil
}

// deleteObsoleteObjects takes template objects of the current tier and of the new tier (provided as newObjects param),
// compares their names and GVKs and deletes those ones that are in the current template but are not found in the new one.
// return `true, nil` if an object was deleted, `false, nil`/`false, err` otherwise
func deleteObsoleteObjects(ctx context.Context, client runtimeclient.Client, currentObjs []runtimeclient.Object, newObjects []runtimeclient.Object) error {
	logger := log.FromContext(ctx)
	logger.Info("looking for obsolete objects", "count", len(currentObjs))
Current:
	for _, currentObj := range currentObjs {
		objectLogger := logger.WithValues("objectName", currentObj.GetObjectKind().GroupVersionKind().Kind+"/"+currentObj.GetName())
		objectLogger.Info("checking obsolete object", "object_namespace", currentObj.GetNamespace(), "object_name", currentObj.GetObjectKind().GroupVersionKind().Kind+"/"+currentObj.GetName())
		for _, newObj := range newObjects {
			if commonclient.SameGVKandName(currentObj, newObj) {
				continue Current
			}
		}
		if err := client.Delete(ctx, currentObj); err != nil && !errors.IsNotFound(err) { // ignore if the object was already deleted
			return errs.Wrapf(err, "failed to delete obsolete object '%s' of kind '%s' in namespace '%s'", currentObj.GetName(), currentObj.GetObjectKind().GroupVersionKind().Kind, currentObj.GetNamespace())
		} else if errors.IsNotFound(err) {
			continue // continue to the next object since this one was already deleted
		}
		logger.Info("deleted obsolete object", "object_namespace", currentObj.GetNamespace(), "object_name", currentObj.GetObjectKind().GroupVersionKind().Kind+"/"+currentObj.GetName())
	}
	return nil
}

// listBySpaceLabel returns runtimeclient.ListOption that filters by label toolchain.dev.openshift.com/space equal to the given spacename
func listBySpaceLabel(spacename string) runtimeclient.ListOption {
	labels := map[string]string{toolchainv1alpha1.SpaceLabelKey: spacename}
	return runtimeclient.MatchingLabels(labels)
}
