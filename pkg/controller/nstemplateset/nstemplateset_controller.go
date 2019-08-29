package nstemplateset

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_nstemplateset")

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNSTemplateSet{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("nstemplateset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{})
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
	client client.Client
	scheme *runtime.Scheme
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
	return r.ensureNamespaces(reqLogger, nsTmplSet)
}

func (r *ReconcileNSTemplateSet) ensureNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	userName := nsTmplSet.GetName()
	labels := map[string]string{
		"owner": userName,
	}
	opts := client.MatchingLabels(labels)
	namespaces := &corev1.NamespaceList{}
	if err := r.client.List(context.TODO(), opts, namespaces); err != nil {
		// TODO status handling
		return reconcile.Result{}, err
	}

	missingNs := findNsForProvision(namespaces.Items, nsTmplSet.Spec.Namespaces, userName)
	if missingNs != (toolchainv1alpha1.Namespace{}) {
		// TODO call template processing for ns
		log.Info("provisioning namespace", "namespace", missingNs)

		// set labels
		nsName := toNamespaceName(userName, missingNs.Type)
		namespace := &corev1.Namespace{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: nsName}, namespace); err != nil {
			return reconcile.Result{}, err
		}
		if err := controllerutil.SetControllerReference(nsTmplSet, namespace, r.scheme); err != nil {
			return reconcile.Result{}, err
		}
		if namespace.Labels == nil {
			namespace.Labels = make(map[string]string)
		}
		namespace.Labels["owner"] = userName
		namespace.Labels["revision"] = missingNs.Revision
		if err := r.client.Update(context.TODO(), namespace); err != nil {
			return reconcile.Result{}, err
		}
		log.Info("namespace provisioned", "namespace", missingNs)
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
