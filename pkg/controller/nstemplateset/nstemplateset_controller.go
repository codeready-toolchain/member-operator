package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/pkg/controller"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"

	"github.com/go-logr/logr"
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
		getTemplateContent: getTemplateContentFromHost,
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

	return nil
}

var _ reconcile.Reconciler = &NSTemplateSetReconciler{}

// NSTemplateSetReconciler the NSTemplateSet reconciler
type NSTemplateSetReconciler struct {
	client             client.Client
	scheme             *runtime.Scheme
	getTemplateContent func(tierName, typeName string) (*templatev1.Template, error)
}

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *NSTemplateSetReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	var err error
	namespace, err := getNamespaceName(request)
	if err != nil {
		reqLogger.Error(err, "failed to determine resource namespace")
		return reconcile.Result{}, err
	}

	// Fetch the NSTemplateSet instance
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: request.Name}, nsTmplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		reqLogger.Error(err, "failed to get NSTemplateSet")
		return reconcile.Result{}, err
	}
	if util.IsBeingDeleted(nsTmplSet) {
		// if the NSTemplateSet has no finalizer, then we don't have anything to do
		if len(nsTmplSet.Finalizers) == 0 {
			reqLogger.Info("NSTemplateSet resource is terminated")
			return reconcile.Result{}, nil
		}
		// since the NSTmplSet resource is being deleted, we must set its status to `ready=false/reason=terminating`
		err := r.setStatusTerminating(nsTmplSet)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, nsTmplSet, r.setStatusDeletingFailed, err, "failed to set status to 'ready=false/reason=terminating' on NSTemplateSet")
		}
		// now, we can delete all "child" namespaces explicitly
		username := nsTmplSet.GetName()
		userNamespaces, err := r.fetchUserNamespaces(username)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, nsTmplSet, r.setStatusDeletingFailed, err, "failed to list namespace with label owner '%s'", username)
		}
		// delete the first namespace which (still) exists and is not in a terminating state
		reqLogger.Info("checking user namepaces associated with the deleted NSTemplateSet...")
		for _, userNS := range userNamespaces {
			if !util.IsBeingDeleted(&userNS) {
				reqLogger.Info("deleting a user namepace associated with the deleted NSTemplateSet", "namespace", userNS.Name)
				if err := r.client.Delete(context.TODO(), &userNS); err != nil {
					return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, nsTmplSet, r.setStatusDeletingFailed, err, "failed to delete user namespace '%s'", userNS.Name)
				}
				return reconcile.Result{}, nil
			}
		}
		// if no namespace was to be deleted, then we can remove the finalizer and we're done
		reqLogger.Info("NSTemplateSet resource is ready to be terminated: all related user namespaces have been marked for deletion")
		nsTmplSet.SetFinalizers([]string{})
		if err := r.client.Update(context.TODO(), nsTmplSet); err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, nsTmplSet, r.setStatusDeletingFailed, err, "failed to remove finalier on NSTemplateSet '%s'", nsTmplSet.Name)
		}
		return reconcile.Result{}, nil
	}

	done, err := r.ensureUserNamespaces(reqLogger, nsTmplSet)
	if !done || err != nil {
		if err != nil {
			reqLogger.Error(err, "failed to provision user namespaces")
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, r.setStatusReady(nsTmplSet)
}

func (r *NSTemplateSetReconciler) fetchUserNamespaces(nsTemplateSetName string) ([]corev1.Namespace, error) {
	// fetch all namespace with owner=username label
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: nsTemplateSetName}
	opts := client.MatchingLabels(labels)
	userNamespaceList := &corev1.NamespaceList{}
	if err := r.client.List(context.TODO(), userNamespaceList, opts); err != nil {
		return nil, err
	}
	return userNamespaceList.Items, nil

}

func (r *NSTemplateSetReconciler) ensureUserNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	username := nsTmplSet.GetName()
	userNamespaces, err := r.fetchUserNamespaces(username)
	if err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err, "failed to list namespace with label owner '%s'", username)
	}
	// find next namespace for provisioning namespace resource
	tcNamespace, userNamespace, found := nextNamespaceToProvision(nsTmplSet.Spec.Namespaces, userNamespaces)
	if !found {
		return true, nil
	}

	// create namespace resource
	return false, r.ensureNamespace(logger, nsTmplSet, tcNamespace, userNamespace)
}

func (r *NSTemplateSetReconciler) ensureNamespace(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, userNamespace *corev1.Namespace) error {
	username := nsTmplSet.GetName()

	log.Info("provisioning namespace", "namespace", tcNamespace)
	if err := r.setStatusProvisioning(nsTmplSet); err != nil {
		return err
	}

	params := map[string]string{"USERNAME": username}

	if userNamespace == nil {
		return r.ensureNamespaceResource(logger, nsTmplSet, tcNamespace, params)
	}
	return r.ensureInnerNamespaceResources(logger, nsTmplSet, tcNamespace, params, userNamespace)
}

func (r *NSTemplateSetReconciler) ensureNamespaceResource(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, params map[string]string) error {
	username := nsTmplSet.GetName()

	tmpl, err := r.getTemplateContent(nsTmplSet.Spec.TierName, tcNamespace.Type)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to to retrieve template for namespace type '%s'", tcNamespace.Type)
	}

	tmplProcessor := template.NewProcessor(r.client, r.scheme)
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
		labels[toolchainv1alpha1.OwnerLabelKey] = username
		labels[toolchainv1alpha1.TypeLabelKey] = tcNamespace.Type
		acc.SetLabels(labels)

		// Note: we don't see an owner reference between the NSTemplateSet (namespaced resource) and the namespace (cluster-wide resource)
		// because a namespaced resource cannot be the owner of a cluster resource (the GC will delete the child resource, considering it is an orphan resource)
		// As a consequence, when the NSTemplateSet is deleted, we explicitly delete the associated namespaces that belong to the same user.
		// see https://issues.redhat.com/browse/CRT-429

	}

	err = tmplProcessor.Apply(objs)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to create namespace with type '%s'", tcNamespace.Type)
	}

	log.Info("namespace provisioned", "namespace", tcNamespace)
	return nil
}

func (r *NSTemplateSetReconciler) ensureInnerNamespaceResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, params map[string]string, namespace *corev1.Namespace) error {
	nsName := namespace.GetName()

	tmplContent, err := r.getTemplateContent(nsTmplSet.Spec.TierName, tcNamespace.Type)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to to retrieve template for namespace '%s'", nsName)
	}

	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	objs, err := tmplProcessor.Process(tmplContent, params, template.RetainAllButNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to process template for namespace '%s'", nsName)
	}
	err = tmplProcessor.Apply(objs)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s' with required resources", nsName)
	}

	if namespace.Labels == nil {
		namespace.Labels = make(map[string]string)
	}
	namespace.Labels[toolchainv1alpha1.RevisionLabelKey] = tcNamespace.Revision
	if err := r.client.Update(context.TODO(), namespace); err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to update namespace '%s'", nsName)
	}

	log.Info("namespace provisioned with required resources", "namespace", tcNamespace)

	// TODO add validation for other objects
	return nil
}

// nextNamespaceToProvision returns first namespace (from given namespaces) with
// namespace status is active and revision not set
// or namespace present in tcNamespaces but not found in given namespaces
func nextNamespaceToProvision(tcNamespaces []toolchainv1alpha1.NSTemplateSetNamespace, namespaces []corev1.Namespace) (*toolchainv1alpha1.NSTemplateSetNamespace, *corev1.Namespace, bool) {
	for _, tcNamespace := range tcNamespaces {
		namespace, found := findNamespace(namespaces, tcNamespace.Type)
		if found {
			if namespace.Status.Phase == corev1.NamespaceActive && namespace.Labels[toolchainv1alpha1.RevisionLabelKey] == "" {
				return &tcNamespace, &namespace, true
			}
		} else {
			return &tcNamespace, nil, true
		}
	}
	return nil, nil, false
}

func findNamespace(namespaces []corev1.Namespace, typeName string) (corev1.Namespace, bool) {
	for _, ns := range namespaces {
		if ns.Labels[toolchainv1alpha1.TypeLabelKey] == typeName {
			return ns, true
		}
	}
	return corev1.Namespace{}, false
}

func getTemplateContentFromHost(tierName, typeName string) (*templatev1.Template, error) {
	templates, err := getNSTemplates(cluster.GetHostCluster, tierName)
	if err != nil {
		return nil, err
	}
	tmpl := templates[typeName].Template
	return &tmpl, nil
}

func getNamespaceName(request reconcile.Request) (string, error) {
	namespace := request.Namespace
	if namespace == "" {
		return k8sutil.GetWatchNamespace()
	}
	return namespace, nil
}

// error handling methods

func (r *NSTemplateSetReconciler) wrapErrorWithStatusUpdate(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, statusUpdater func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(nsTmplSet, err.Error()); err != nil {
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

func (r *NSTemplateSetReconciler) setStatusDeletingFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionReason,
			Message: message,
		})
}

func (r *NSTemplateSetReconciler) setStatusProvisioning(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetProvisioningReason,
		})
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

func (r *NSTemplateSetReconciler) setStatusTerminating(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
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
