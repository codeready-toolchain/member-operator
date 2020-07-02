package idler

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"

	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_idler")

// Add creates a new Idler Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, config *configuration.Config) error {
	return add(mgr, newReconciler(mgr, config))
}

func newReconciler(mgr manager.Manager, config *configuration.Config) reconcile.Reconciler {
	return &ReconcileIdler{client: mgr.GetClient(), scheme: mgr.GetScheme(), config: config}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	c, err := controller.New("idler-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	log.Info("!!!!! reconciling Idler 0")
	// Watch for changes to primary resource Idler
	if err := c.Watch(&source.Kind{Type: &toolchainv1alpha1.Idler{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

	log.Info("!!!!! reconciling Idler 2")
	// Watch for changes to secondary resources: Pods
	if err := c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileIdler{}

// ReconcileIdler reconciles an Idler object
type ReconcileIdler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	config *configuration.Config
}

// Reconcile reads that state of the cluster for an Idler object and makes changes based on the state read
// and what is in the Idler.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileIdler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	var err error

	logger.Info("!!!!! reconciling Idler 1")

	// Fetch the Idler instance
	idler := &toolchainv1alpha1.Idler{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: request.Name}, idler)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to get Idler")
		return reconcile.Result{}, err
	}
	if !util.IsBeingDeleted(idler) {
		logger.Info("ensuring idling")
		if err = r.ensureIdling(logger, idler); err != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileIdler) ensureIdling(logger logr.Logger, idler *toolchainv1alpha1.Idler) error {
	// List all namespaces by the user owner for this idler
	namespaceList := &corev1.NamespaceList{}
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: idler.Name}
	if err := r.client.List(context.TODO(), namespaceList, client.MatchingLabels(labels)); err != nil {
		return err
	}

	for _, userNamespace := range namespaceList.Items {
		if err := r.ensureIdlingForNamespace(logger, userNamespace.Name); err != nil {
			return err
		}
	}

	return nil
}

func (r *ReconcileIdler) ensureIdlingForNamespace(logger logr.Logger, namespace string) error {
	podList := &corev1.PodList{}
	if err := r.client.List(context.TODO(), podList, &client.ListOptions{Namespace: namespace}); err != nil {
		return err
	}
	for _, pod := range podList.Items {
		// TODO
		logger.Info("pod", "name", pod.Name, "phase", pod.Status.Phase)
	}
	return nil
}

type statusUpdater func(idler *toolchainv1alpha1.Idler, message string) error

// wrapErrorWithStatusUpdate wraps the error and update the idler status. If the update failed then logs the error.
func (r *ReconcileIdler) wrapErrorWithStatusUpdate(logger logr.Logger, idler *toolchainv1alpha1.Idler, statusUpdater statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(idler, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}
