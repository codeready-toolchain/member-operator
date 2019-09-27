package nstemplateset

import (
	"context"

	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	templatev1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("controller_nstemplateset")

const (
	// Status condition reasons
	unableToProvisionReason          = "UnableToProvision"
	unableToProvisionNamespaceReason = "UnableToProvisionNamespace"
	provisioningReason               = "Provisioning"
	provisionedReason                = "Provisioned"
)

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNSTemplateSet{
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

	// Watch for changes to primary resource
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource
	enqueueRequestForOwner := &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &toolchainv1alpha1.NSTemplateSet{},
	}
	if err := c.Watch(&source.Kind{Type: &corev1.Namespace{}}, enqueueRequestForOwner); err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileNSTemplateSet{}

type ReconcileNSTemplateSet struct {
	client             client.Client
	scheme             *runtime.Scheme
	getTemplateContent func(tierName, typeName string) (*templatev1.Template, error)
}

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *ReconcileNSTemplateSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	var err error
	namespace := request.Namespace
	if namespace == "" {
		namespace, err = k8sutil.GetWatchNamespace()
		if err != nil {
			reqLogger.Error(err, "failed to determine resource namespace")
			return reconcile.Result{}, err
		}
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

	done, err := r.ensureUserNamespaces(reqLogger, nsTmplSet)
	if !done || err != nil {
		if err != nil {
			reqLogger.Error(err, "failed to provision user namespaces")
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, r.setStatusReady(nsTmplSet)
}

func (r *ReconcileNSTemplateSet) ensureUserNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (bool, error) {
	username := nsTmplSet.GetName()

	// fetch all namespace with owner=username label
	labels := map[string]string{"owner": username}
	opts := client.MatchingLabels(labels)
	userNamespaceList := &corev1.NamespaceList{}
	if err := r.client.List(context.TODO(), opts, userNamespaceList); err != nil {
		return false, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err,
			"failed to list namespace with label owner '%s'", username)
	}
	userNamespaces := userNamespaceList.Items

	// find next namespace for provisioning namespace resource
	tcNamespace, userNamespace, found := nextNamespaceToProvision(nsTmplSet.Spec.Namespaces, userNamespaces)
	if !found {
		return true, nil
	}

	// create namespace resource
	return false, r.ensureNamespace(logger, nsTmplSet, tcNamespace, userNamespace)
}

func (r *ReconcileNSTemplateSet) ensureNamespace(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, userNamespace *corev1.Namespace) error {
	username := nsTmplSet.GetName()

	log.Info("provisioning namespace", "namespace", tcNamespace)
	if err := r.setStatusProvisioning(nsTmplSet); err != nil {
		return err
	}

	params := map[string]string{"USER_NAME": username}

	if userNamespace == nil {
		return r.ensureNamespaceResource(logger, nsTmplSet, tcNamespace, params)
	}
	return r.ensureInnerNamespaceResources(logger, nsTmplSet, tcNamespace, params, userNamespace)
}

func (r *ReconcileNSTemplateSet) ensureNamespaceResource(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, params map[string]string) error {
	username := nsTmplSet.GetName()

	tmpl, err := r.getTemplateContent(nsTmplSet.Spec.TierName, tcNamespace.Type)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to to retrieve template for namespace type '%s'", tcNamespace.Type)
	}

	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	objs, err := tmplProcessor.Process(tmpl, params, template.RetainNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to process template for namespace type '%s'", tcNamespace.Type)
	}

	for _, rawObj := range objs {
		acc, err := meta.Accessor(rawObj.Object)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
				"invalid element in template for namespace type '%s'", tcNamespace.Type)
		}

		// set labels
		labels := acc.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["owner"] = username
		labels["type"] = tcNamespace.Type
		acc.SetLabels(labels)

		// set owner ref
		if err := controllerutil.SetControllerReference(nsTmplSet, acc, r.scheme); err != nil {
			return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
				"failed to set controller reference for namespace type '%s'", tcNamespace.Type)
		}
	}

	err = tmplProcessor.Apply(objs)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to create namespace with type '%s'", tcNamespace.Type)
	}

	log.Info("namespace provisioned", "namespace", tcNamespace)
	return nil
}

func (r *ReconcileNSTemplateSet) ensureInnerNamespaceResources(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, tcNamespace *toolchainv1alpha1.NSTemplateSetNamespace, params map[string]string, namespace *corev1.Namespace) error {
	nsName := namespace.GetName()

	tmplContent, err := r.getTemplateContent(nsTmplSet.Spec.TierName, tcNamespace.Type)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to to retrieve template for namespace '%s'", nsName)
	}

	tmplProcessor := template.NewProcessor(r.client, r.scheme)
	objs, err := tmplProcessor.Process(tmplContent, params, template.RetainAllButNamespaces)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to process template for namespace '%s'", nsName)
	}
	err = tmplProcessor.Apply(objs)
	if err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to provision namespace '%s'", nsName)
	}

	if namespace.Labels == nil {
		namespace.Labels = make(map[string]string)
	}
	namespace.Labels["revision"] = tcNamespace.Revision
	if err := r.client.Update(context.TODO(), namespace); err != nil {
		return r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err,
			"failed to update namespace '%s'", nsName)
	}

	log.Info("namespace provisioned", "namespace", tcNamespace)

	// TODO add validation for other objects
	return nil
}

// nextNamespaceToProvision returns first namespace (from given namespaces) with
// namespace status is active and revision not set
// or namesapce present in tcNamespaces but not found in given namespaces
func nextNamespaceToProvision(tcNamespaces []toolchainv1alpha1.NSTemplateSetNamespace, namespaces []corev1.Namespace) (*toolchainv1alpha1.NSTemplateSetNamespace, *corev1.Namespace, bool) {
	for _, tcNamespace := range tcNamespaces {
		namespace, found := findNamespace(namespaces, tcNamespace.Type)
		if found {
			if namespace.Status.Phase == corev1.NamespaceActive && namespace.GetLabels()["revision"] == "" {
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
		if ns.GetLabels()["type"] == typeName {
			return ns, true
		}
	}
	return corev1.Namespace{}, false
}

func getTemplateContentFromHost(tierName, typeName string) (*templatev1.Template, error) {
	templates, err := template.GetNSTemplates(cluster.GetHostCluster, tierName)
	if err != nil {
		return nil, err
	}
	tmpl := templates[typeName].Template
	return &tmpl, nil
}

// error handling methods

func (r *ReconcileNSTemplateSet) wrapErrorWithStatusUpdate(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet, statusUpdater func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(nsTmplSet, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *ReconcileNSTemplateSet) updateStatusConditions(nsTmplSet *toolchainv1alpha1.NSTemplateSet, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	nsTmplSet.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(nsTmplSet.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), nsTmplSet)
}

func (r *ReconcileNSTemplateSet) setStatusProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  unableToProvisionReason,
			Message: message,
		})
}

func (r *ReconcileNSTemplateSet) setStatusProvisioning(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: provisioningReason,
		})
}

func (r *ReconcileNSTemplateSet) setStatusReady(nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: provisionedReason,
		})
}

func (r *ReconcileNSTemplateSet) setStatusNamespaceProvisionFailed(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  unableToProvisionNamespaceReason,
			Message: message,
		})
}
