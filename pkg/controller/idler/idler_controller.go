package idler

import (
	"context"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"

	"github.com/go-logr/logr"
	openshiftappsv1 "github.com/openshift/api/apps/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
func Add(mgr manager.Manager, cl client.Client) error {
	return add(mgr, newReconciler(mgr, cl))
}

func newReconciler(mgr manager.Manager, cl client.Client) reconcile.Reconciler {
	return &ReconcileIdler{client: cl, scheme: mgr.GetScheme()}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	c, err := controller.New("idler-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Idler
	if err := c.Watch(&source.Kind{Type: &toolchainv1alpha1.Idler{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

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
}

// Reconcile reads that state of the cluster for an Idler object and makes changes based on the state read
// and what is in the Idler.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileIdler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Fetch the Idler instance
	idler := &toolchainv1alpha1.Idler{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: request.Name}, idler); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "failed to get Idler")
		return reconcile.Result{}, err
	}
	if util.IsBeingDeleted(idler) {
		return reconcile.Result{}, nil
	}

	logger.Info("ensuring idling")
	if idler.Spec.TimeoutSeconds < 1 {
		// Make sure the timeout is bigger than 0
		err := errs.New("timeoutSeconds should be bigger than 0")
		logger.Error(err, "failed to ensure idling")
		return reconcile.Result{}, r.setStatusFailed(idler, err.Error())
	}
	if err := r.ensureIdling(logger, idler); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, idler, r.setStatusFailed, err,
			"failed to ensure idling '%s'", idler.Name)
	}
	// Find the earlier pod to kill and requeue. Do not requeue if no pods tracked
	d := nextPodToBeKilledAfter(idler)
	if d == nil || *d < time.Duration(0) {
		// No pods tracked. Requeue after the idler timout so we don't miss new pods created within the timeout.
		timeout := time.Duration(idler.Spec.TimeoutSeconds) * time.Second
		d = &timeout
	}
	log.Info("requeueing in", "duration", d)
	return reconcile.Result{
		Requeue:      true,
		RequeueAfter: *d,
	}, r.setStatusReady(idler)
}

func (r *ReconcileIdler) ensureIdling(logger logr.Logger, idler *toolchainv1alpha1.Idler) error {
	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.client.List(context.TODO(), podList, &client.ListOptions{Namespace: idler.Name}); err != nil {
		return err
	}
	newStatusPods := make([]toolchainv1alpha1.Pod, 0, 10)
	for _, pod := range podList.Items {
		podLogger := logger.WithValues("Pod.Name", pod.Name)
		podLogger.Info("Pod", "Pod.Phase", pod.Status.Phase)
		if trackedPod := findPodByName(idler, pod.Name); trackedPod != nil {
			// Already tracking this pod. Check the timeout.
			if time.Now().After(trackedPod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds) * time.Second)) {
				podLogger.Info("Pod running for too long. Killing the pod.")
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				deletedByController, err := r.scaleControllerToZero(podLogger, pod.ObjectMeta)
				if err != nil {
					return err
				}
				if !deletedByController { // Pod not managed by a controller. We can just delete the pod.
					logger.Info("Deleting pod without controller")
					if err := r.client.Delete(context.TODO(), &pod); err != nil {
						return err
					}
					podLogger.Info("Pod deleted")
				}
			} else {
				newStatusPods = append(newStatusPods, *trackedPod) // keep tracking
			}
		} else if pod.Status.StartTime != nil { // Ignore pods without StartTime
			podLogger.Info("New pod detected. Start tracking.")
			newStatusPods = append(newStatusPods, toolchainv1alpha1.Pod{
				Name:      pod.Name,
				StartTime: *pod.Status.StartTime,
			})
		}
	}

	return r.updateStatusPods(idler, newStatusPods)
}

// scaleControllerToZero checks if the object has an owner controller (Deployment, ReplicaSet, etc)
// and scales the owner down to zero and returns "true".
// Otherwise returns "false".
func (r *ReconcileIdler) scaleControllerToZero(logger logr.Logger, meta metav1.ObjectMeta) (bool, error) {
	logger.Info("Scaling controller to zero", "name", meta.Name)
	owners := meta.GetOwnerReferences()
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			log.Info("owner", "owner", owner.Kind)
			switch owner.Kind {
			case "Deployment":
				return r.scaleDeploymentToZero(logger, meta.Namespace, owner)
			case "ReplicaSet":
				return r.scaleReplicaSetToZero(logger, meta.Namespace, owner)
			case "DaemonSet":
				return r.deleteDaemonSet(logger, meta.Namespace, owner) // Nothing to scale down. Delete instead.
			case "StatefulSet":
				return r.scaleStatefulSetToZero(logger, meta.Namespace, owner)
			case "DeploymentConfig":
				return r.scaleDeploymentConfigToZero(logger, meta.Namespace, owner)
			case "ReplicationController":
				return r.scaleReplicationControllerToZero(logger, meta.Namespace, owner)
			case "Job":
				return r.deleteJob(logger, meta.Namespace, owner) // Nothing to scale down. Delete instead.
			}
		}
	}
	return false, nil
}

func (r *ReconcileIdler) scaleDeploymentToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	logger.Info("Scaling deployment to zero", "name", owner.Name)
	d := &appsv1.Deployment{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, d); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	var zero int32 = 0
	d.Spec.Replicas = &zero
	if err := r.client.Update(context.TODO(), d); err != nil {
		return false, err
	}
	logger.Info("Deployment scaled to zero", "name", d.Name)
	return true, nil
}

func (r *ReconcileIdler) scaleReplicaSetToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	logger.Info("Scaling replica set to zero", "name", owner.Name)
	rs := &appsv1.ReplicaSet{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, rs); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			log.Error(err, "replica set is not found; ignoring: it might be already deleted")
			return true, nil
		}
		log.Error(err, "error deleting rs")
		return false, err
	}
	deletedByController, err := r.scaleControllerToZero(logger, rs.ObjectMeta) // Check if the ReplicaSet has another controller which owns it (i.g. Deployment)
	if err != nil {
		return false, err
	}
	if !deletedByController {
		// There is no controller that owns the ReplicaSet. Scale the ReplicaSet to zero.
		var zero int32 = 0
		rs.Spec.Replicas = &zero
		if err := r.client.Update(context.TODO(), rs); err != nil {
			return false, err
		}
		logger.Info("ReplicaSet scaled to zero", "name", rs.Name)
	}
	return true, nil
}

func (r *ReconcileIdler) deleteDaemonSet(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	ds := &appsv1.DaemonSet{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	if err := r.client.Delete(context.TODO(), ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	logger.Info("DaemonSet deleted", "name", ds.Name)
	return true, nil
}

func (r *ReconcileIdler) scaleStatefulSetToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	s := &appsv1.StatefulSet{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, s); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	var zero int32 = 0
	s.Spec.Replicas = &zero
	if err := r.client.Update(context.TODO(), s); err != nil {
		return false, err
	}
	logger.Info("StatefulSet scaled to zero", "name", s.Name)
	return true, nil
}

func (r *ReconcileIdler) scaleDeploymentConfigToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	dc := &openshiftappsv1.DeploymentConfig{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, dc); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	dc.Spec.Replicas = 0
	if err := r.client.Update(context.TODO(), dc); err != nil {
		return false, err
	}
	logger.Info("DeploymentConfig scaled to zero", "name", dc.Name)
	return true, nil
}

func (r *ReconcileIdler) scaleReplicationControllerToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	rc := &corev1.ReplicationController{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, rc); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	deletedByController, err := r.scaleControllerToZero(logger, rc.ObjectMeta) // Check if the ReplicationController has another controller which owns it (i.g. DeploymentConfig)
	if err != nil {
		return false, err
	}
	if !deletedByController {
		// There is no controller who owns the ReplicationController. Scale the ReplicationController to zero.
		var zero int32 = 0
		rc.Spec.Replicas = &zero
		if err := r.client.Update(context.TODO(), rc); err != nil {
			return false, err
		}
		logger.Info("ReplicationController scaled to zero", "name", rc.Name)
	}
	return true, nil
}

func (r *ReconcileIdler) deleteJob(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	j := &batchv1.Job{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, j); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	if err := r.client.Delete(context.TODO(), j); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	logger.Info("Job deleted", "name", j.Name)
	return true, nil
}

func findPodByName(idler *toolchainv1alpha1.Idler, name string) *toolchainv1alpha1.Pod {
	for _, pod := range idler.Status.Pods {
		if pod.Name == name {
			return &pod
		}
	}
	return nil
}

// nextPodToBeKilledAfter checks the start times of all the tracked pods in the Idler and returns the timeout left
// for the next pod to be killed.
func nextPodToBeKilledAfter(idler *toolchainv1alpha1.Idler) *time.Duration {
	if len(idler.Status.Pods) == 0 {
		return nil
	}
	var d time.Duration
	for _, pod := range idler.Status.Pods {
		whenToKill := pod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second)
		killAfter := whenToKill.Sub(time.Now())
		if d == 0 || killAfter < d {
			d = killAfter
		}
	}
	return &d
}

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
	idler.Status.Pods = newPods
	return r.client.Status().Update(context.TODO(), idler)
}

type statusUpdater func(idler *toolchainv1alpha1.Idler, message string) error

func (r *ReconcileIdler) updateStatusConditions(idler *toolchainv1alpha1.Idler, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	idler.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(idler.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), idler)
}

func (r *ReconcileIdler) setStatusFailed(idler *toolchainv1alpha1.Idler, message string) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.IdlerUnableToEnsureIdlingReason,
			Message: message,
		})
}

func (r *ReconcileIdler) setStatusReady(idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerRunningReason,
		})
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
