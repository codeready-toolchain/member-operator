package nstemplateset

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	"k8s.io/apimachinery/pkg/api/errors"
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
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("controller_nstemplateset")

const (
	// Status condition reasons
	unableToProvisionReason          = "UnableToProvision"
	unableToProvisionNamespaceReason = "UnableToProvisionNamespace"
	provisioningNamespaceReason      = "ProvisioningNamespace"
	provisioningReason               = "Provisioning"
	provisionedReason                = "Provisioned"
)

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNSTemplateSet{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		applyTemplate: applyTemplate,
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
	client        client.Client
	scheme        *runtime.Scheme
	applyTemplate func(client.Client, toolchainv1alpha1.Namespace, map[string]string) error
}

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *ReconcileNSTemplateSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	// Fetch the NSTemplateSet instance
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, nsTmplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	result, err := r.ensureNamespaces(reqLogger, nsTmplSet)
	if err != nil || result.Requeue == true {
		return result, err
	}
	return result, r.setStatusReady(nsTmplSet)
}

func (r *ReconcileNSTemplateSet) ensureNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	userName := nsTmplSet.GetName()
	labels := map[string]string{
		"owner": userName,
	}
	opts := client.MatchingLabels(labels)
	namespaces := &corev1.NamespaceList{}
	if err := r.client.List(context.TODO(), opts, namespaces); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusProvisionFailed, err, "failed to list namespace with label owner '%s'", userName)
	}

	missingNs := findNsForProvision(namespaces.Items, nsTmplSet.Spec.Namespaces, userName)
	if missingNs != (toolchainv1alpha1.Namespace{}) {
		nsName := toNamespaceName(userName, missingNs.Type)
		log.Info("provisioning namespace", "namespace", missingNs)
		if err := r.setStatusNamespaceProvisioning(nsTmplSet, nsName); err != nil {
			return reconcile.Result{}, err
		}

		params := make(map[string]string)
		params["USER_NAME"] = userName
		err := r.applyTemplate(r.client, missingNs, params)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to provision namespace '%s'", nsName)
		}

		// set labels
		namespace := &corev1.Namespace{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: nsName}, namespace); err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to get namespace '%s'", nsName)
		}
		if err := controllerutil.SetControllerReference(nsTmplSet, namespace, r.scheme); err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to set controller '%s'", nsName)
		}
		if namespace.Labels == nil {
			namespace.Labels = make(map[string]string)
		}
		namespace.Labels["owner"] = userName
		namespace.Labels["revision"] = missingNs.Revision
		if err := r.client.Update(context.TODO(), namespace); err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, nsTmplSet, r.setStatusNamespaceProvisionFailed, err, "failed to update namespace '%s'", nsName)
		}
		log.Info("namespace provisioned", "namespace", missingNs)
		if err := r.setStatusProvisioning(nsTmplSet); err != nil {
			return reconcile.Result{}, err
		}
		time.Sleep(time.Second * 5)
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcile.Result{}, nil
}

func findNsForProvision(namespaces []corev1.Namespace, tcNamespaces []toolchainv1alpha1.Namespace, userName string) toolchainv1alpha1.Namespace {
	for _, tcNamespace := range tcNamespaces {
		nsName := toNamespaceName(userName, tcNamespace.Type)
		found := findNamespace(namespaces, nsName, tcNamespace.Revision)
		if !found {
			return tcNamespace
		}
	}
	return toolchainv1alpha1.Namespace{}
}

func findNamespace(namespaces []corev1.Namespace, namespaceName, revision string) bool {
	for _, ns := range namespaces {
		if ns.GetName() == namespaceName && ns.GetLabels()["revision"] == revision {
			return true
		}
	}
	return false
}

func toNamespaceName(userName, nsType string) string {
	return fmt.Sprintf("%s-%s", userName, nsType)
}

func applyTemplate(client client.Client, tcNamespace toolchainv1alpha1.Namespace, params map[string]string) error {
	// TODO get template content from template tier
	// TODO apply template with template content to create namesapce
	return nil
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

func (r *ReconcileNSTemplateSet) setStatusNamespaceProvisioning(nsTmplSet *toolchainv1alpha1.NSTemplateSet, namespaceName string) error {
	return r.updateStatusConditions(
		nsTmplSet,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  provisioningNamespaceReason,
			Message: fmt.Sprintf("provisioning %s namespace", namespaceName),
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
