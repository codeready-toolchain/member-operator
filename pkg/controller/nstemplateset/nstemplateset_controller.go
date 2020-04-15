package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/pkg/controller"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"

	"github.com/go-logr/logr"
	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
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
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &NSTemplateSetReconciler{
		client:             mgr.GetClient(),
		scheme:             mgr.GetScheme(),
		getTemplateContent: getTemplateFromHost,
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

// NSTemplateSetReconciler the NSTemplateSet reconciler
type NSTemplateSetReconciler struct {
	client             client.Client
	scheme             *runtime.Scheme
	getTemplateContent TemplateContentProvider
}

// TemplateContentProvider a function that returns a template for a gven tier and type
type TemplateContentProvider func(tierName, typeName string) (*templatev1.Template, error)

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
	if createdOrUpdated, err := r.ensureClusterResources(logger, nsTmplSet); err != nil {
		return reconcile.Result{}, err
	} else if createdOrUpdated {
		return reconcile.Result{}, nil // wait for cluster resources to be created
	}

	done, err := r.ensureNamespaces(logger, nsTmplSet)
	if err != nil {
		logger.Error(err, "failed to provision user namespaces")
		return reconcile.Result{}, err
	} else if !done {
		return reconcile.Result{}, nil // just wait until namespaces change to "active" phase
	}

	return reconcile.Result{}, r.setStatusReady(nsTmplSet)
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
	if err := r.setStatusTerminating(nsTmplSet); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to set status to 'ready=false/reason=terminating' on NSTemplateSet")
	}
	username := nsTmplSet.GetName()

	// now, we can delete all "child" namespaces explicitly
	userNamespaces, err := r.fetchNamespaces(username)
	if err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list namespace with label owner '%s'", username)
	}
	// delete the first namespace which (still) exists and is not in a terminating state
	logger.Info("checking user namepaces associated with the deleted NSTemplateSet...")
	for _, ns := range userNamespaces {
		if !util.IsBeingDeleted(&ns) {
			logger.Info("deleting a user namepace associated with the deleted NSTemplateSet", "namespace", ns.Name)
			if err := r.client.Delete(context.TODO(), &ns); err != nil {
				return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete user namespace '%s'", ns.Name)
			}
			return reconcile.Result{}, nil
		}
	}

	// if no namespace was to be deleted, then we can proceed with the cluster resources associated with the user
	objs, err := r.getTemplateObjects(nsTmplSet.Spec.TierName, ClusterResources, username)
	if err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to list cluster resources for user '%s'", username)
	}
	logger.Info("listed cluster resources to delete", "count", len(objs))
	for _, obj := range objs {
		objMeta, err := meta.Accessor(obj.Object)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource of kind '%s'", obj.Object.GetObjectKind())
		}
		// ignore cluster resource that are already flagged for deletion
		if objMeta.GetDeletionTimestamp() != nil {
			continue
		}
		logger.Info("deleting cluster resource", "name", objMeta.GetName())
		err = r.client.Delete(context.TODO(), obj.Object)
		if err != nil && errors.IsNotFound(err) {
			// ignore case where the resource did not exist anymore, move to the next one to delete
			continue
		} else if err != nil {
			// report an error only if the resource could not be deleted (but ignore if the resource did not exist anymore)
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to delete cluster resource '%s'", objMeta.GetName())
		}
		// stop there for now. Will reconcile again for the next cluster resource (if any exists)
		return reconcile.Result{}, nil
	}

	// if nothing was to be deleted, then we can remove the finalizer and we're done
	logger.Info("NSTemplateSet resource is ready to be terminated: all related user namespaces have been marked for deletion")
	util.RemoveFinalizer(nsTmplSet, toolchainv1alpha1.FinalizerName)
	if err := r.client.Update(context.TODO(), nsTmplSet); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusTerminatingFailed, err, "failed to remove finalier on NSTemplateSet '%s'", username)
	}
	return reconcile.Result{}, nil
}

// fetchNamespaces returns all current namespaces belonging to the given user
// i.e., labeled with `"toolchain.dev.openshift.com/owner":<username>`
func (r *NSTemplateSetReconciler) fetchNamespaces(username string) ([]corev1.Namespace, error) {
	// fetch all namespace with owner=username label
	userNamespaceList := &corev1.NamespaceList{}
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: username}
	if err := r.client.List(context.TODO(), userNamespaceList, client.MatchingLabels(labels)); err != nil {
		return nil, err
	}
	fmt.Printf("listed namespaces: count=%d\n", len(userNamespaceList.Items))
	return userNamespaceList.Items, nil
}

// ensureClusterResources ensures that the cluster resources exists.
// Returns `true, nil` if something was created or updated, `false, nil` if nothing changed, `true|false, err` if an
// error occurred
func (r *NSTemplateSetReconciler) ensureClusterResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	logger.Info("ensuring cluster resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName)
	username := nsTmplSet.GetName()
	newObjs, err := r.getTemplateObjects(nsTmplSet.Spec.TierName, ClusterResources, username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "failed to retrieve template for the cluster resources")
	}
	if err := r.setStatusUpdating(nsTmplSet); err != nil {
		return false, err
	}
	// let's look for existing cluster resource quotas to determine the current tier
	crqs := quotav1.ClusterResourceQuotaList{}
	if err := r.client.List(context.TODO(), &crqs); err != nil {
		logger.Error(err, "failed to list existing cluster resource quotas")
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to list existing cluster resource quotas")
	} else if len(crqs.Items) > 0 {
		// only if necessary
		crqMeta, err := meta.Accessor(&(crqs.Items[0]))
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant cluster resources")
		}
		if currentTier, exists := crqMeta.GetLabels()[toolchainv1alpha1.TierLabelKey]; exists {
			if err := r.deleteRedundantObjects(logger, currentTier, ClusterResources, username, newObjs); err != nil {
				logger.Error(err, "failed to delete redundant cluster resources")
				return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant cluster resources")
			}
		}
	}

	for _, rawObj := range newObjs {
		acc, err := meta.Accessor(rawObj.Object)
		if err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "invalid element in template for the cluster resources")
		}

		// set labels
		labels := acc.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[toolchainv1alpha1.OwnerLabelKey] = nsTmplSet.GetName()
		labels[toolchainv1alpha1.TypeLabelKey] = ClusterResources
		labels[toolchainv1alpha1.RevisionLabelKey] = nsTmplSet.Spec.ClusterResources.Revision
		labels[toolchainv1alpha1.TierLabelKey] = nsTmplSet.Spec.TierName
		labels[toolchainv1alpha1.ProviderLabelKey] = toolchainv1alpha1.ProviderLabelValue
		acc.SetLabels(labels)

		// Note: we don't set an owner reference between the NSTemplateSet (namespaced resource) and the cluster-wide resources
		// because a namespaced resource (NSTemplateSet) cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
		// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated namespaces that belong to the same user.
		// see https://issues.redhat.com/browse/CRT-429
	}

	if createdOrUpdated, err := template.NewProcessor(r.client, r.scheme).Apply(newObjs); err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusClusterResourcesProvisionFailed, err, "failed to create cluster resources")
	} else if createdOrUpdated {
		logger.Info("provisioned cluster resources")
		return true, r.setStatusProvisioning(nsTmplSet, "provisioning cluster resources")
	}
	logger.Info("cluster resources already provisioned")
	return false, nil
}

func (r *NSTemplateSetReconciler) ensureNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	username := nsTmplSet.GetName()
	userNamespaces, err := r.fetchNamespaces(username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err, "failed to list namespaces with label owner '%s'", username)
	}

	toDeprovision, found := nextNamespaceToDeprovision(nsTmplSet.Spec.Namespaces, userNamespaces)
	if found {
		if err := r.setStatusUpdating(nsTmplSet); err != nil {
			return false, err
		}
		if err := r.client.Delete(context.TODO(), toDeprovision); err != nil {
			return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete namespace %s", toDeprovision.Name)
		}
		logger.Info("deleted namespace as part of NSTemplateSet update", "namespace", toDeprovision.Name)
		return false, nil
	}

	// find next namespace for provisioning namespace resource
	tcNamespace, userNamespace, found := nextNamespaceToProvisionOrUpdate(nsTmplSet, userNamespaces)
	if !found {
		logger.Info("no more namespaces to create", "username", nsTmplSet.GetName())
		return true, nil
	}

	// create namespace resource
	return false, r.ensureNamespace(logger, nsTmplSet, tcNamespace, userNamespace)
}

func (r *NSTemplateSetReconciler) ensureNamespace(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, userNamespace *corev1.Namespace) error {
	logger.Info("provisioning namespace", "namespace", tcNamespace.Type, "tier", nsTmplSet.Spec.TierName)
	if err := r.setStatusProvisioning(nsTmplSet, fmt.Sprintf("provisioning the '-%s' namespace", tcNamespace.Type)); err != nil {
		return err
	}
	// create namespace before created inner resources because creating the namespace may take some time
	if userNamespace == nil {
		return r.ensureNamespaceResource(logger, nsTmplSet, tcNamespace)
	}
	return r.ensureInnerNamespaceResources(logger, nsTmplSet, tcNamespace, userNamespace)
}

func (r *NSTemplateSetReconciler) ensureNamespaceResource(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace) error {
	logger.Info("creating namespace", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName, "type", tcNamespace.Type)
	tmpl, err := r.getTemplateContent(nsTmplSet.Spec.TierName, tcNamespace.Type)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to retrieve template for namespace type '%s'", tcNamespace.Type)
	}
	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	params := map[string]string{"USERNAME": nsTmplSet.GetName()}
	objs, err := tmplProcessor.Process(tmpl, params, template.RetainNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to process template for namespace type '%s'", tcNamespace.Type)
	}

	for _, rawObj := range objs {
		acc, err := meta.Accessor(rawObj.Object)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "invalid element in template for namespace type '%s'", tcNamespace.Type)
		}

		// set labels
		labels := acc.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[toolchainv1alpha1.OwnerLabelKey] = nsTmplSet.GetName()
		labels[toolchainv1alpha1.TypeLabelKey] = tcNamespace.Type
		labels[toolchainv1alpha1.ProviderLabelKey] = toolchainv1alpha1.ProviderLabelValue

		acc.SetLabels(labels)

		// Note: we don't see an owner reference between the NSTemplateSet (namespaced resource) and the namespace (cluster-wide resource)
		// because a namespaced resource cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
		// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated namespaces that belong to the same user.
		// see https://issues.redhat.com/browse/CRT-429

	}

	_, err = tmplProcessor.Apply(objs)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to create namespace with type '%s'", tcNamespace.Type)
	}

	logger.Info("namespace provisioned", "namespace", tcNamespace)
	return nil
}

func (r *NSTemplateSetReconciler) ensureInnerNamespaceResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, namespace *corev1.Namespace) error {
	logger.Info("creating namespace resources", "username", nsTmplSet.GetName(), "tier", nsTmplSet.Spec.TierName, "type", tcNamespace.Type)
	nsName := namespace.GetName()
	username := nsTmplSet.GetName()
	newObjs, err := r.getTemplateObjects(nsTmplSet.Spec.TierName, tcNamespace.Type, username, template.RetainAllButNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to process template for namespace '%s'", nsName)
	}

	if currentTier, exists := namespace.Labels[toolchainv1alpha1.TierLabelKey]; !exists || currentTier != nsTmplSet.Spec.TierName {
		if err := r.setStatusUpdating(nsTmplSet); err != nil {
			return err
		}
		if err := r.deleteRedundantObjects(logger, currentTier, tcNamespace.Type, username, newObjs); err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusUpdateFailed, err, "failed to delete redundant objects in namespace '%s'", nsName)
		}
	}

	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	for _, rawObj := range newObjs {
		acc, err := meta.Accessor(rawObj.Object)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "unable to get meta.Interface of the object '%s' in the namespace '%s'", rawObj.Raw, nsName)
		}
		// add the "toolchain.dev.openshift.com/provider: codeready-toolchain" label on all objects
		labels := acc.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[toolchainv1alpha1.ProviderLabelKey] = toolchainv1alpha1.ProviderLabelValue
		acc.SetLabels(labels)
	}
	_, err = tmplProcessor.Apply(newObjs)

	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s' with required resources", nsName)
	}

	if namespace.Labels == nil {
		namespace.Labels = make(map[string]string)
	}

	// Adding labels indicating that the namespace is up-to-date with revision/tier
	namespace.Labels[toolchainv1alpha1.RevisionLabelKey] = tcNamespace.Revision
	namespace.Labels[toolchainv1alpha1.TierLabelKey] = nsTmplSet.Spec.TierName
	if err := r.client.Update(context.TODO(), namespace); err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to update namespace '%s'", nsName)
	}

	logger.Info("namespace provisioned with required resources", "tier", nsTmplSet.Spec.TierName, "namespace", tcNamespace)

	// TODO add validation for other objects
	return nil
}

// deleteRedundantObjects takes template objects of the current tier and of the new tier (provided as newObjects param),
// compares their names and GVKs and deletes those ones that are in the current template but are not found in the new one.
func (r *NSTemplateSetReconciler) deleteRedundantObjects(logger logr.Logger, currentTier, typeName, username string, newObjects []runtime.RawExtension) error {
	currentObjs, err := r.getTemplateObjects(currentTier, typeName, username, template.RetainAllButNamespaces)
	if err != nil {
		return errs.Wrapf(err, "failed to retrieve template for tier/type '%s/%s'", currentTier, typeName)
	}
	logger.Info("checking redundant objects", "tier", currentTier, "count", len(currentObjs))
Current:
	for _, currentObj := range currentObjs {
		current, err := meta.Accessor(currentObj.Object)
		if err != nil {
			return err
		}
		logger.Info("checking redundant object", "objectName", currentObj.Object.GetObjectKind().GroupVersionKind().Kind+"/"+current.GetName())
		for _, newObj := range newObjects {
			newOb, err := meta.Accessor(newObj.Object)
			if err != nil {
				return err
			}
			if current.GetName() == newOb.GetName() &&
				currentObj.Object.GetObjectKind().GroupVersionKind() == newObj.Object.GetObjectKind().GroupVersionKind() {
				continue Current
			}
		}
		if err := r.client.Delete(context.TODO(), currentObj.Object); err != nil {
			return errs.Wrapf(err, "failed to delete object '%s' of kind '%s' in namespace '%s'", current.GetName(), currentObj.Object.GetObjectKind().GroupVersionKind().Kind, current.GetNamespace())
		}
		logger.Info("deleted redundant object", "objectName", currentObj.Object.GetObjectKind().GroupVersionKind().Kind+"/"+current.GetName())
	}
	return nil
}

// nextNamespaceToProvisionOrUpdate returns first namespace (from given namespaces) whose status is active and
// either revision is not set or revision or tier doesn't equal to the current one.
// It also returns namespace present in tcNamespaces but not found in given namespaces
func nextNamespaceToProvisionOrUpdate(nsTmplSet *toolchainv1alpha1.NSTemplateSet, namespaces []corev1.Namespace) (*toolchainv1alpha1.NSTemplateSetNamespace, *corev1.Namespace, bool) {
	for _, tcNamespace := range nsTmplSet.Spec.Namespaces {
		namespace, found := findNamespace(namespaces, tcNamespace.Type)
		if found {
			if namespace.Status.Phase == corev1.NamespaceActive {
				if namespace.Labels[toolchainv1alpha1.RevisionLabelKey] == "" ||
					namespace.Labels[toolchainv1alpha1.RevisionLabelKey] != tcNamespace.Revision ||
					namespace.Labels[toolchainv1alpha1.TierLabelKey] != nsTmplSet.Spec.TierName {
					return &tcNamespace, &namespace, true
				}
			}
		} else {
			return &tcNamespace, nil, true
		}
	}
	return nil, nil, false
}

// nextNamespaceToDeprovision returns namespace (and information of it was found) that should be deprovisioned
// because its type wasn't found in the set of namespace types in NSTemplateSet
func nextNamespaceToDeprovision(tcNamespaces []toolchainv1alpha1.NSTemplateSetNamespace, namespaces []corev1.Namespace) (*corev1.Namespace, bool) {
Namespaces:
	for _, ns := range namespaces {
		for _, tcNs := range tcNamespaces {
			if tcNs.Type == ns.Labels[toolchainv1alpha1.TypeLabelKey] {
				continue Namespaces
			}
		}
		return &ns, true
	}
	return nil, false
}

func findNamespace(namespaces []corev1.Namespace, typeName string) (corev1.Namespace, bool) {
	for _, ns := range namespaces {
		if ns.Labels[toolchainv1alpha1.TypeLabelKey] == typeName {
			return ns, true
		}
	}
	return corev1.Namespace{}, false
}

func getNamespaceName(request reconcile.Request) (string, error) {
	namespace := request.Namespace
	if namespace == "" {
		return k8sutil.GetWatchNamespace()
	}
	return namespace, nil
}

func (r *NSTemplateSetReconciler) getTemplateObjects(tierName, typeName, username string, filters ...template.FilterFunc) ([]runtime.RawExtension, error) {
	tmplContent, err := r.getTemplateContent(tierName, typeName)
	if err != nil {
		return nil, err
	}
	if tmplContent == nil {
		return nil, nil
	}
	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	params := map[string]string{"USERNAME": username}
	return tmplProcessor.Process(tmplContent, params, filters...)
}

// error handling methods
type statusUpdater func(*toolchainv1alpha1.NSTemplateSet, string) error

func (r *NSTemplateSetReconciler) wrapErrorWithStatusUpdate(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, updateStatus statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := updateStatus(nsTmplSet, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *NSTemplateSetReconciler) updateStatusConditions(nsTmplSet *toolchainv1alpha1.NSTemplateSet, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	nsTmplSet.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(nsTmplSet.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), nsTmplSet)
}

func (r *NSTemplateSetReconciler) setStatusReady(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.NSTemplateSetProvisionedReason,
		})
}

func (r *NSTemplateSetReconciler) setStatusProvisioning(nsTmplSet *toolchainv1alpha1.NSTemplateSet, msg string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetProvisioningReason,
			Message: msg,
		})
}

func (r *NSTemplateSetReconciler) setStatusProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionReason,
			Message: message,
		})
}

func (r *NSTemplateSetReconciler) setStatusNamespaceProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
			Message: message,
		})
}

func (r *NSTemplateSetReconciler) setStatusClusterResourcesProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionClusterResourcesReason,
			Message: message,
		})
}

func (r *NSTemplateSetReconciler) setStatusTerminating(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
		})
}

func (r *NSTemplateSetReconciler) setStatusUpdating(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetUpdatingReason,
		})
}

func (r *NSTemplateSetReconciler) setStatusUpdateFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUpdateFailedReason,
			Message: message,
		})
}

func (r *NSTemplateSetReconciler) setStatusTerminatingFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetTerminatingFailedReason,
			Message: message,
		})
}
