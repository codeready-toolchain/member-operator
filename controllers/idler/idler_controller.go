package idler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/client-go/discovery"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	notify "github.com/codeready-toolchain/toolchain-common/pkg/notification"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/scale"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	runtimeCluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	restartThreshold = 50
	// Keep the AAP pod restart threshold lower than the default so the AAP idler kicks in before the main idler.
	aapRestartThreshold = restartThreshold - 1
	vmSubresourceURLFmt = "/apis/subresources.kubevirt.io/%s"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager, allNamespaceCluster runtimeCluster.Cluster) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.Idler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(source.Kind(allNamespaceCluster.GetCache(), &corev1.Pod{},
			handler.TypedEnqueueRequestsFromMapFunc(MapPodToIdler), PodIdlerPredicate{})).
		Complete(r)
}

// Reconciler reconciles an Idler object
type Reconciler struct {
	Client              client.Client
	Scheme              *runtime.Scheme
	AllNamespacesClient client.Client
	RestClient          rest.Interface
	ScalesClient        scale.ScalesGetter
	DynamicClient       dynamic.Interface
	DiscoveryClient     discovery.ServerResourcesInterface
	GetHostCluster      cluster.GetHostClusterFunc
	Namespace           string
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=idlers/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=pods;replicationcontrollers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances;datavolumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices;servingruntimes,verbs=get;list;watch;create;update;patch;delete

// needed to stop the VMs - we need to make a PUT request for the "stop" subresource. Kubernetes internally classifies these as either create or update
// based on the state of the existing object.
//+kubebuilder:rbac:groups=subresources.kubevirt.io,resources=virtualmachines/stop,verbs=create;update

//+kubebuilder:rbac:groups=aap.ansible.com,resources=ansibleautomationplatforms,verbs=get;list;watch;create;update;patch;delete
// There are other AAP resource kinds which are involved in the Pod -> ... -> AnsibleAutomationPlatform ownership chain. We need to be able to get/list them.
//+kubebuilder:rbac:groups=aap.ansible.com,resources=*,verbs=get;list

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
	if err := r.Client.Get(ctx, types.NamespacedName{Name: request.Name}, idler); err != nil {
		if apierrors.IsNotFound(err) {
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
	if idler.Spec.TimeoutSeconds == 0 {
		logger.Info("no idling when timeout is 0")
		return reconcile.Result{}, r.setStatusNoDeactivation(ctx, idler)
	}
	if idler.Spec.TimeoutSeconds < 0 {
		// Make sure the timeout is bigger than 0
		err := errors.New("timeoutSeconds should be bigger than 0")
		logger.Error(err, "failed to ensure idling")
		return reconcile.Result{}, r.setStatusFailed(ctx, idler, err.Error())
	}
	requeueAfter, err := r.ensureIdling(ctx, idler)
	if err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(ctx, idler, r.setStatusFailed, err,
			"failed to ensure idling '%s'", idler.Name)
	}
	logger.Info("requeueing for next pod to check", "after_seconds", requeueAfter.Seconds())
	result := reconcile.Result{
		Requeue:      true,
		RequeueAfter: requeueAfter,
	}
	return result, r.setStatusReady(ctx, idler)
}

func getTimeout(idler *toolchainv1alpha1.Idler, pod corev1.Pod) int32 {
	timeoutSeconds := idler.Spec.TimeoutSeconds
	if isOwnedByVM(pod.ObjectMeta) {
		// use 1/12th of the timeout for VMs to have more aggressive idling to decrease
		// the infra costs because VMs consume much more resources
		timeoutSeconds = timeoutSeconds / 12
	}
	return timeoutSeconds
}

func (r *Reconciler) ensureIdling(ctx context.Context, idler *toolchainv1alpha1.Idler) (time.Duration, error) {
	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.AllNamespacesClient.List(ctx, podList, client.InNamespace(idler.Name)); err != nil {
		return 0, err
	}
	ownerIdler := newOwnerIdler(idler, r)
	requeueAfter := time.Duration(idler.Spec.TimeoutSeconds) * time.Second
	var idleErrors []error
	for _, pod := range podList.Items {
		podLogger := log.FromContext(ctx).WithValues("pod_name", pod.Name, "pod_phase", pod.Status.Phase)
		podCtx := log.IntoContext(ctx, podLogger)

		timeoutSeconds := getTimeout(idler, pod)
		if pod.Status.StartTime != nil {
			// check the restart count for the pod
			restartCount := getHighestRestartCount(pod.Status)
			if restartCount > restartThreshold {
				podLogger.Info("Pod is restarting too often. Killing the pod", "restart_count", restartCount)
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				err := r.deletePodsAndCreateNotification(podCtx, pod, idler, ownerIdler)
				if err == nil {
					continue
				}
				idleErrors = append(idleErrors, err)
				podLogger.Error(err, "failed to kill the pod")
			}
			// Check the start time
			if time.Now().After(pod.Status.StartTime.Add(time.Duration(timeoutSeconds) * time.Second)) {
				podLogger.Info("Pod running for too long. Killing the pod.", "start_time", pod.Status.StartTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", timeoutSeconds)
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				err := r.deletePodsAndCreateNotification(podCtx, pod, idler, ownerIdler)
				if err == nil {
					requeueAfter = shorterDuration(requeueAfter, time.Duration(float32(timeoutSeconds)*0.05)*time.Second)
					continue
				}
				idleErrors = append(idleErrors, err)
				podLogger.Error(err, "failed to kill the pod")
			}
		}
		// calculate the next reconcile
		if pod.Status.StartTime != nil {
			killAfter := time.Until(pod.Status.StartTime.Add(time.Duration(timeoutSeconds+1) * time.Second))
			requeueAfter = shorterDuration(requeueAfter, killAfter)
		} else {
			// if the pod doesn't contain startTime, then schedule the next reconcile to the timeout
			// if not already scheduled to an earlier time
			requeueAfter = shorterDuration(requeueAfter, time.Duration(timeoutSeconds)*time.Second)
		}
	}
	return requeueAfter, errors.Join(idleErrors...)
}

func shorterDuration(first, second time.Duration) time.Duration {
	shorter := first
	if first > second {
		shorter = second
	}
	// do not allow negative durations: if a pod has timed out, then it should be killed immediately
	if shorter < 0 {
		shorter = 0
	}
	return shorter
}

// Check if the pod belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
// if it is a standalone pod, delete it.
// Send notification if the deleted pod was managed by a controller, was a standalone pod that was not completed or was crashlooping
func (r *Reconciler) deletePodsAndCreateNotification(podCtx context.Context, pod corev1.Pod, idler *toolchainv1alpha1.Idler, ownerIdler *ownerIdler) error {
	logger := log.FromContext(podCtx)
	isCompleted := false
	for _, podCond := range pod.Status.Conditions {
		if podCond.Type == "Ready" {
			isCompleted = podCond.Reason == "PodCompleted"
			break
		}
	}
	appType, appName, err := ownerIdler.scaleOwnerToZero(podCtx, &pod)
	if err != nil {
		if apierrors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return nil
		}
		return err
	}
	// when appType is empty, then it no known controller was found
	deletedByController := appType != ""
	isEvicted := pod.Status.Reason == "Evicted"
	if !deletedByController || isCompleted || isEvicted { // Pod not managed by a controller, or completed or evicted pod. We can just delete the pod.
		logger.Info("Deleting pod", "managed-by-controller", deletedByController, "completed", isCompleted, "evicted", isEvicted)
		if err := r.AllNamespacesClient.Delete(podCtx, &pod); err != nil {
			return err
		}
		logger.Info("Pod deleted")
	}
	if appName == "" {
		appName = pod.Name
		appType = "Pod"
	}

	// If the pod was in the completed state (it wasn't running) and there was no controller scaled down,
	// then  there's no reason to send an idler notification
	if !isCompleted || deletedByController {
		// By now either a pod has been deleted or scaled to zero by controller, idler Triggered notification should be sent
		r.notify(podCtx, idler, appName, appType)
	}
	return nil
}

func getHighestRestartCount(podstatus corev1.PodStatus) int32 {
	var restartCount int32
	for _, status := range podstatus.ContainerStatuses {
		if restartCount < status.RestartCount {
			restartCount = status.RestartCount
		}
	}
	return restartCount
}

func (r *Reconciler) notify(ctx context.Context, idler *toolchainv1alpha1.Idler, appName string, appType string) {
	logger := log.FromContext(ctx)
	logger.Info("Creating Notification")
	if err := r.createNotification(ctx, idler, appName, appType); err != nil {
		logger.Error(err, "failed to create Notification")
		if err = r.setStatusIdlerNotificationCreationFailed(ctx, idler, err.Error()); err != nil {
			logger.Error(err, "failed to set status IdlerNotificationCreationFailed")
		} // not returning error to continue processing remaining pods
	}
}

func (r *Reconciler) createNotification(ctx context.Context, idler *toolchainv1alpha1.Idler, appName string, appType string) error {
	//Get the HostClient
	hostCluster, ok := r.GetHostCluster()
	if !ok {
		return fmt.Errorf("unable to get the host cluster")
	}
	//check the condition on Idler if notification already sent, only create a notification if not created before
	if condition.IsTrue(idler.Status.Conditions, toolchainv1alpha1.IdlerTriggeredNotificationCreated) {
		// notification already created
		return nil
	}

	notificationName := fmt.Sprintf("%s-%s", idler.Name, toolchainv1alpha1.NotificationTypeIdled)
	notification := &toolchainv1alpha1.Notification{}
	// Check if notification already exists in host
	if err := hostCluster.Client.Get(ctx, types.NamespacedName{Name: notificationName, Namespace: hostCluster.OperatorNamespace}, notification); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		userEmails, err := r.getUserEmailsFromMURs(ctx, hostCluster, idler)
		if err != nil {
			return err
		}
		if len(userEmails) == 0 {
			// no email found, thus no email sent
			return fmt.Errorf("no email found for the user in MURs")
		}

		keysAndVals := map[string]string{
			"Namespace": idler.Name,
			"AppName":   appName,
			"AppType":   appType,
		}

		for _, userEmail := range userEmails {
			_, err := notify.NewNotificationBuilder(hostCluster.Client, hostCluster.OperatorNamespace).
				WithName(notificationName).
				WithNotificationType(toolchainv1alpha1.NotificationTypeIdled).
				WithTemplate("idlertriggered").
				WithKeysAndValues(keysAndVals).
				Create(ctx, userEmail)
			if err != nil {
				return fmt.Errorf("unable to create Notification CR from Idler: %w", err)
			}
		}
	}
	// set notification created condition
	return r.setStatusIdlerNotificationCreated(ctx, idler)
}

func (r *Reconciler) getUserEmailsFromMURs(ctx context.Context, hostCluster *cluster.CachedToolchainCluster, idler *toolchainv1alpha1.Idler) ([]string, error) {
	var emails []string
	//get NSTemplateSet from idler
	logger := log.FromContext(ctx)
	if spacename, found := idler.GetLabels()[toolchainv1alpha1.SpaceLabelKey]; found {
		nsTemplateSet := &toolchainv1alpha1.NSTemplateSet{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: spacename, Namespace: r.Namespace}, nsTemplateSet)
		if err != nil {
			logger.Error(err, "could not get the NSTemplateSet with name", "spacename", spacename)
			return emails, err
		}
		// iterate on space roles from NSTemplateSet
		var murs []string
		for _, spaceRole := range nsTemplateSet.Spec.SpaceRoles {
			murs = append(murs, spaceRole.Usernames...)
		}
		// get MUR from host and use user email from annotations
		for _, mur := range murs {
			getMUR := &toolchainv1alpha1.MasterUserRecord{}
			err := hostCluster.Client.Get(ctx, types.NamespacedName{Name: mur, Namespace: hostCluster.OperatorNamespace}, getMUR)
			if err != nil {
				return emails, fmt.Errorf("could not get the MUR: %w", err)
			}
			if email := getMUR.Spec.PropagatedClaims.Email; email != "" {
				emails = append(emails, getMUR.Spec.PropagatedClaims.Email)
			}
		}
	} else {
		logger.Info("Idler does not have any owner label", "idler_name", idler.Name)
	}
	return emails, nil
}

type statusUpdater func(ctx context.Context, idler *toolchainv1alpha1.Idler, message string) error

func (r *Reconciler) updateStatusConditions(ctx context.Context, idler *toolchainv1alpha1.Idler, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	idler.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(idler.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.Client.Status().Update(ctx, idler)
}

func (r *Reconciler) setStatusFailed(ctx context.Context, idler *toolchainv1alpha1.Idler, message string) error {
	return r.updateStatusConditions(
		ctx,
		idler,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.IdlerUnableToEnsureIdlingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusReady(ctx context.Context, idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		ctx,
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerRunningReason,
		})
}

func (r *Reconciler) setStatusNoDeactivation(ctx context.Context, idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		ctx,
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerNoDeactivationReason,
		})
}

func (r *Reconciler) setStatusIdlerNotificationCreated(ctx context.Context, idler *toolchainv1alpha1.Idler) error {
	return r.updateStatusConditions(
		ctx,
		idler,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.IdlerTriggeredNotificationCreated,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.IdlerTriggeredReason,
		})
}

func (r *Reconciler) setStatusIdlerNotificationCreationFailed(ctx context.Context, idler *toolchainv1alpha1.Idler, message string) error {
	return r.updateStatusConditions(
		ctx,
		idler,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.IdlerTriggeredNotificationCreated,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.IdlerTriggeredNotificationCreationFailedReason,
			Message: message,
		})
}

// wrapErrorWithStatusUpdate wraps the error and update the idler status. If the update failed then logs the error.
func (r *Reconciler) wrapErrorWithStatusUpdate(ctx context.Context, idler *toolchainv1alpha1.Idler, statusUpdater statusUpdater, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(ctx, idler, err.Error()); err != nil {
		log.FromContext(ctx).Error(err, "status update failed")
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

func isOwnedByVM(meta metav1.ObjectMeta) bool {
	owners := meta.GetOwnerReferences()
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			if owner.Kind == "VirtualMachineInstance" {
				return true
			}
		}
	}
	return false
}
