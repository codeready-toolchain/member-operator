package idler

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	openshiftappsv1 "github.com/openshift/api/apps/v1"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	notify "github.com/codeready-toolchain/toolchain-common/pkg/notification"
)

const (
	MemberOperatorNS = "MEMBER_OPERATOR_NAMESPACE"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.Idler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconciler reconciles an Idler object
type Reconciler struct {
	Client              client.Client
	Scheme              *runtime.Scheme
	AllNamespacesClient client.Client
	GetHostCluster      func() (*cluster.CachedToolchainCluster, bool)
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=pods;replicationcontrollers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile reads that state of the cluster for an Idler object and makes changes based on the state read
// and what is in the Idler.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
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
	//if err := r.createNotification(logger, idler); err != nil {
	//	return reconcile.Result{}, r.setStatusIdlerNotificationCreationFailed(idler, err.Error())
	//}
	// Find the earlier pod to kill and requeue. Do not requeue if no pods tracked
	nextTime := nextPodToBeKilledAfter(logger, idler)
	if nextTime == nil {
		after := time.Duration(idler.Spec.TimeoutSeconds) * time.Second
		logger.Info("requeueing for next pod to check", "after_seconds", after.Seconds())
		return reconcile.Result{
			Requeue:      true,
			RequeueAfter: after,
		}, r.setStatusReady(idler)

	}
	logger.Info("requeueing for next pod to kill", "after_seconds", nextTime.Seconds())
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
		podLogger := logger.WithValues("pod_name", pod.Name, "pod_phase", pod.Status.Phase)
		if trackedPod := findPodByName(idler, pod.Name); trackedPod != nil {
			// Already tracking this pod. Check the timeout.
			if time.Now().After(trackedPod.StartTime.Add(time.Duration(idler.Spec.TimeoutSeconds) * time.Second)) {
				podLogger.Info("Pod running for too long. Killing the pod.", "start_time", trackedPod.StartTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", idler.Spec.TimeoutSeconds)
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				deletedByController, err := r.scaleControllerToZero(podLogger, pod.ObjectMeta)
				if err != nil {
					return err
				}
				if !deletedByController { // Pod not managed by a controller. We can just delete the pod.
					logger.Info("Deleting pod without controller")
					if err := r.AllNamespacesClient.Delete(context.TODO(), &pod); err != nil { // nolint:gosec
						return err
					}
					podLogger.Info("Pod deleted")
				}
				if err := r.createNotification(logger, idler); err != nil {
					return r.setStatusIdlerNotificationCreationFailed(idler, err.Error())
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

func (r *Reconciler) createNotification(logger logr.Logger, idler *toolchainv1alpha1.Idler) error {
	//Get the HostClient
	hostCluster, ok := r.GetHostCluster()
	if !ok {
		err := fmt.Errorf("unable to get the host cluster")
		logger.Error(err, "host Cluster not found")
		return err
	}
	// add sending notification here
	//check the condition on Idler if notification already sent
	_, found := condition.FindConditionByType(idler.Status.Conditions, toolchainv1alpha1.IdlerActivatedNotificationCreated)
	if !found {
		userEmails := r.getUserEmailFromUserSignup(logger, hostCluster, idler)
		// Only create a notification if not created before
		for _, userEmail := range userEmails {
			_, err := notify.NewNotificationBuilder(hostCluster.Client, hostCluster.OperatorNamespace).
				WithNotificationType(toolchainv1alpha1.NotificationTypeIdled).
				WithControllerReference(idler, r.Scheme).
				WithTemplate("idleractivated").
				Create(userEmail)
			if err != nil {
				return errs.Wrapf(err, "Unable to create Notification CR from Idler")
			}
		}
		// update Condition
		if err := r.setStatusIdlerNotificationCreated(idler); err != nil {
			return err
		}
	}
	//notification already created
	return nil
}

func (r *Reconciler) getUserEmailFromUserSignup(logger logr.Logger, hostCluster *cluster.CachedToolchainCluster, idler *toolchainv1alpha1.Idler) []string {
	var emails []string
	//get NSTemplateSet from idler
	owner, found := idler.GetLabels()[toolchainv1alpha1.OwnerLabelKey]
	if found {
		nsTemplateSet := &toolchainv1alpha1.NSTemplateSet{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: owner, Namespace: MemberOperatorNS}, nsTemplateSet)
		if err != nil {
			logger.Info(fmt.Sprintf(" Could not get the NSTemplateSet with name: %s", owner), err)
			return emails
		}
		//get MUR from NSTemplateSetSpec
		spaceRoles := nsTemplateSet.Spec.SpaceRoles
		var murs []string
		for _, spaceRole := range spaceRoles {
			murs = append(murs, spaceRole.Usernames...)
		}
		// get MUR from host and use user email from annotations
		for _, mur := range murs {
			getMUR := &toolchainv1alpha1.MasterUserRecord{}
			err := hostCluster.Client.Get(context.TODO(), types.NamespacedName{Name: mur, Namespace: hostCluster.OperatorNamespace}, getMUR)
			if err != nil {
				logger.Info(fmt.Sprintf("Could not get the MUR with name : %s", mur), err)
				continue
			}
			emails = append(emails, getMUR.Annotations[toolchainv1alpha1.MasterUserRecordEmailAnnotationKey])
		}
	}
	return emails
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
	zero := int32(0)
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
			logger.Error(err, "replica set is not found; ignoring: it might be already deleted")
			return true, nil
		}
		logger.Error(err, "error deleting rs")
		return false, err
	}
	deletedByController, err := r.scaleControllerToZero(logger, rs.ObjectMeta) // Check if the ReplicaSet has another controller which owns it (i.g. Deployment)
	if err != nil {
		return false, err
	}
	if !deletedByController {
		// There is no controller that owns the ReplicaSet. Scale the ReplicaSet to zero.
		zero := int32(0)
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
	zero := int32(0)
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
		zero := int32(0)
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

func (r *Reconciler) setStatusIdlerNotificationCreated(idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.IdlerActivatedNotificationCreated,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerActivated,
		})
}

func (r *Reconciler) setStatusIdlerNotificationCreationFailed(idler *toolchainv1alpha1.Idler, message string) error {
	return r.updateStatusConditions(
		idler,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.IdlerActivatedNotificationCreated,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.IdlerActivatedNotificationCreationFailed,
			Message: message,
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
