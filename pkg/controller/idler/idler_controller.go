package idler

import (
	"context"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"

	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	if err := c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(func(a handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Name: a.Meta.GetNamespace(), // Use pod's namespace name as the name of the corresponding Idler resource
				}},
			}
		}),
	}); err != nil {
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

	logger.Info("!!!!! reconciling Idler 1")

	// Fetch the Idler instance
	idler := &toolchainv1alpha1.Idler{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: request.Name}, idler); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to get Idler")
		return reconcile.Result{}, err
	}
	if !util.IsBeingDeleted(idler) {
		logger.Info("ensuring idling")
		if err := r.ensureIdling(logger, idler); err != nil {
			return reconcile.Result{}, err
		}
		// Find the earlier pod to kill and requeue
		return reconcile.Result{
			Requeue:      true,
			RequeueAfter: nextPodToBeKilledAfter(idler),
		}, nil
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileIdler) ensureIdling(logger logr.Logger, idler *toolchainv1alpha1.Idler) error {
	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.client.List(context.TODO(), podList, &client.ListOptions{Namespace: idler.Name}); err != nil {
		return err
	}
	newStatusPods := make([]toolchainv1alpha1.Pod, 0, 10)
	for _, pod := range podList.Items {
		logger.Info("pod", "name", pod.Name, "phase", pod.Status.Phase)
		if trackedPod := findPodByName(idler, pod.Name); trackedPod != nil {
			// Already tracking this pod. Check the timeout.
			newStatusPods = append(newStatusPods, *trackedPod) // keep tracking
			if time.Now().After(trackedPod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds) * time.Second)) {
				// Running too long. Kill the pod.
				// TODO Check if it belongs to a replica set (or Deployment or DC) and scale it down to zero. Otherwise kill it.
			}
		} else if pod.Status.StartTime != nil { // Ignore pods without StartTime
			// New pod. Start tracking.
			newStatusPods = append(newStatusPods, toolchainv1alpha1.Pod{
				Name:      pod.Name,
				StartTime: *pod.Status.StartTime,
			})
		}
	}

	return r.updateStatusPods(idler, newStatusPods)
}

func findPodByName(idler *toolchainv1alpha1.Idler, name string) *toolchainv1alpha1.Pod {
	for _, pod := range idler.Status.Pods {
		if pod.Name == name {
			return &pod
		}
	}
	return nil
}

func nextPodToBeKilledAfter(idler *toolchainv1alpha1.Idler) time.Duration {
	var d time.Duration
	for _, pod := range idler.Status.Pods {
		whenToKill := pod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second)
		killAfter := whenToKill.Sub(time.Now())
		if d == 0 || killAfter < d {
			d = killAfter
		}
	}
	return d
}

type statusUpdater func(idler *toolchainv1alpha1.Idler, message string) error

// updateStatusPods updates the status pods to the new ones but only if something changed. Order is ignored.
func (r *ReconcileIdler) updateStatusPods(idler *toolchainv1alpha1.Idler, newPods []toolchainv1alpha1.Pod) error {
	nothingChanged := len(idler.Status.Pods) == len(newPods)
	if nothingChanged {
		for _, newPod := range newPods {
			if findPodByName(idler, newPod.Name) == nil {
				// New untracked Pod!
				nothingChanged = false
				break
			}
		}
	}
	if nothingChanged {
		return nil
	}
	return r.client.Status().Update(context.TODO(), idler)
}

func (r *ReconcileIdler) updateStatusConditions(idler *toolchainv1alpha1.Idler, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	idler.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(idler.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), idler)
}

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
