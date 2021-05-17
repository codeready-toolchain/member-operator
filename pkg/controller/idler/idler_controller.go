package idler

import (
	"context"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"k8s.io/apimachinery/pkg/runtime"

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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func add(mgr manager.Manager, r *Reconciler) error {
	c, err := controller.New("idler-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Idler
	if err := c.Watch(
		&source.Kind{Type: &toolchainv1alpha1.Idler{}},
		&handler.EnqueueRequestForObject{},
		predicate.GenerationChangedPredicate{}); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return add(mgr, r)
}

// Reconciler reconciles an Idler object
type Reconciler struct {
	Client              client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	AllNamespacesClient client.Client
}

// Reconcile reads that state of the cluster for an Idler object and makes changes based on the state read
// and what is in the Idler.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *Reconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := r.Log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("new reconcile loop")
	// Fetch the Idler instance
	idler := &toolchainv1alpha1.Idler{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: request.Name}, idler); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("no Idler found for namespace", "name", request.Name)
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
	nextTime := nextPodToBeKilledAfter(r.Log, idler)
	if nextTime == nil {
		r.Log.Info("requeueing for next pod to check", "duration", nextTime)
		after := time.Duration(idler.Spec.TimeoutSeconds) * time.Second
		return reconcile.Result{
			Requeue:      true,
			RequeueAfter: after,
		}, r.setStatusReady(idler)

	}
	r.Log.Info("requeueing for next pod to kill", "duration", nextTime)
	return reconcile.Result{
		Requeue:      true,
		RequeueAfter: *nextTime,
	}, r.setStatusReady(idler)
}

func (r *Reconciler) ensureIdling(logger logr.Logger, idler *toolchainv1alpha1.Idler) error {
	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.AllNamespacesClient.List(context.TODO(), podList, client.InNamespace(idler.Name)); err != nil {
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
					if err := r.AllNamespacesClient.Delete(context.TODO(), &pod); err != nil {
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
func (r *Reconciler) scaleControllerToZero(logger logr.Logger, meta metav1.ObjectMeta) (bool, error) {
	logger.Info("Scaling controller to zero", "name", meta.Name)
	owners := meta.GetOwnerReferences()
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
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

func (r *Reconciler) scaleDeploymentToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	logger.Info("Scaling deployment to zero", "name", owner.Name)
	d := &appsv1.Deployment{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, d); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	var zero int32 = 0
	d.Spec.Replicas = &zero
	if err := r.AllNamespacesClient.Update(context.TODO(), d); err != nil {
		return false, err
	}
	logger.Info("Deployment scaled to zero", "name", d.Name)
	return true, nil
}

func (r *Reconciler) scaleReplicaSetToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	logger.Info("Scaling replica set to zero", "name", owner.Name)
	rs := &appsv1.ReplicaSet{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, rs); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			r.Log.Error(err, "replica set is not found; ignoring: it might be already deleted")
			return true, nil
		}
		r.Log.Error(err, "error deleting rs")
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
		if err := r.AllNamespacesClient.Update(context.TODO(), rs); err != nil {
			return false, err
		}
		logger.Info("ReplicaSet scaled to zero", "name", rs.Name)
	}
	return true, nil
}

func (r *Reconciler) deleteDaemonSet(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	ds := &appsv1.DaemonSet{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	if err := r.AllNamespacesClient.Delete(context.TODO(), ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	logger.Info("DaemonSet deleted", "name", ds.Name)
	return true, nil
}

func (r *Reconciler) scaleStatefulSetToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	s := &appsv1.StatefulSet{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, s); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	var zero int32 = 0
	s.Spec.Replicas = &zero
	if err := r.AllNamespacesClient.Update(context.TODO(), s); err != nil {
		return false, err
	}
	logger.Info("StatefulSet scaled to zero", "name", s.Name)
	return true, nil
}

func (r *Reconciler) scaleDeploymentConfigToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	dc := &openshiftappsv1.DeploymentConfig{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, dc); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return true, nil
		}
		return false, err
	}
	dc.Spec.Replicas = 0
	if err := r.AllNamespacesClient.Update(context.TODO(), dc); err != nil {
		return false, err
	}
	logger.Info("DeploymentConfig scaled to zero", "name", dc.Name)
	return true, nil
}

func (r *Reconciler) scaleReplicationControllerToZero(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	rc := &corev1.ReplicationController{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, rc); err != nil {
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
		if err := r.AllNamespacesClient.Update(context.TODO(), rc); err != nil {
			return false, err
		}
		logger.Info("ReplicationController scaled to zero", "name", rc.Name)
	}
	return true, nil
}

func (r *Reconciler) deleteJob(logger logr.Logger, namespace string, owner metav1.OwnerReference) (bool, error) {
	j := &batchv1.Job{}
	if err := r.AllNamespacesClient.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: owner.Name}, j); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			logger.Info("job not found")
			return true, nil
		}
		return false, err
	}
	// see https://github.com/kubernetes/kubernetes/issues/20902#issuecomment-321484735
	// also, this may be needed for the e2e tests if the call to `client.Delete` comes too quickly after creating the job,
	// which may leave the job's pod running but orphan, hence causing a test failure (and making the test flaky)
	propagationPolicy := metav1.DeletePropagationBackground

	if err := r.AllNamespacesClient.Delete(context.TODO(), j, &client.DeleteOptions{
		PropagationPolicy: &propagationPolicy,
	}); err != nil {
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

// nextPodToBeKilledAfter checks the start times of all the tracked pods in the Idler and the timeout left
// for the next pod to be killed.
// If there is no pod to kill, the func returns `nil`
func nextPodToBeKilledAfter(log logr.Logger, idler *toolchainv1alpha1.Idler) *time.Duration {
	if len(idler.Status.Pods) == 0 {
		// no pod tracked, so nothing to kill
		return nil
	}
	var d time.Duration
	for _, pod := range idler.Status.Pods {
		killAfter := time.Until(pod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds+1) * time.Second))
		if d == 0 || killAfter < d {
			d = killAfter
		}
	}
	// do not allow negative durations: if a pod has timed out, then it should be killed immediately
	if d < 0 {
		d = 0
	}
	log.Info("next pod to kill", "after", d)
	return &d
}

// updateStatusPods updates the status pods to the new ones but only if something changed. Order is ignored.
func (r *Reconciler) updateStatusPods(idler *toolchainv1alpha1.Idler, newPods []toolchainv1alpha1.Pod) error {
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
	return r.Client.Status().Update(context.TODO(), idler)
}

type statusUpdater func(idler *toolchainv1alpha1.Idler, message string) error

func (r *Reconciler) updateStatusConditions(idler *toolchainv1alpha1.Idler, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	idler.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(idler.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.Client.Status().Update(context.TODO(), idler)
}

func (r *Reconciler) setStatusFailed(idler *toolchainv1alpha1.Idler, message string) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.IdlerUnableToEnsureIdlingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusReady(idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerRunningReason,
		})
}

// wrapErrorWithStatusUpdate wraps the error and update the idler status. If the update failed then logs the error.
func (r *Reconciler) wrapErrorWithStatusUpdate(logger logr.Logger, idler *toolchainv1alpha1.Idler, statusUpdater statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(idler, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}
