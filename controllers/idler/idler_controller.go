package idler

import (
	"context"
	"fmt"
	"k8s.io/client-go/discovery"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	notify "github.com/codeready-toolchain/toolchain-common/pkg/notification"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/scale"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

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
)

const (
	RestartThreshold     = 50
	AAPRestartThreshold  = 30 // Keep the AAP pod restart threshold lower than the default so the AAP idler kicks in before the main idler.
	RequeueTimeThreshold = 300 * time.Second
)

var SupportedScaleResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithKind("Integration"):          schema.GroupVersion{Group: "camel.apache.org", Version: "v1"}.WithResource("integrations"),
	schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithKind("KameletBinding"): schema.GroupVersion{Group: "camel.apache.org", Version: "v1alpha1"}.WithResource("kameletbindings"),
}

var vmGVR = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines"}
var vmInstanceGVR = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachineinstances"}

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
	ScalesClient        scale.ScalesGetter
	DynamicClient       dynamic.Interface
	DiscoveryClient     *discovery.DiscoveryClient
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
//+kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances,verbs=get;list;watch;create;update;patch;delete

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
	if idler.Spec.TimeoutSeconds == 0 {
		logger.Info("no idling when timeout is 0")
		return reconcile.Result{}, r.setStatusNoDeactivation(ctx, idler)
	}
	if idler.Spec.TimeoutSeconds < 0 {
		// Make sure the timeout is bigger than 0
		err := errs.New("timeoutSeconds should be bigger than 0")
		logger.Error(err, "failed to ensure idling")
		return reconcile.Result{}, r.setStatusFailed(ctx, idler, err.Error())
	}
	if err := r.ensureIdling(ctx, idler); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(ctx, idler, r.setStatusFailed, err,
			"failed to ensure idling '%s'", idler.Name)
	}
	if err := r.ensureAnsiblePlatformIdling(ctx, idler); err != nil {
		return reconcile.Result{}, r.wrapErrorWithStatusUpdate(ctx, idler, r.setStatusFailed, err,
			"failed to ensure aap idling '%s'", idler.Name)
	}
	// Requeue in shortest of the following values idler.Spec.TimeoutSeconds or RequeueTimeThreshold or nextPodToBeKilledAfter
	after := findShortestRequeueDuration(idler)
	logger.Info("requeueing for next pod to check", "after_seconds", after.Seconds())
	return reconcile.Result{
		Requeue:      true,
		RequeueAfter: after,
	}, r.setStatusReady(ctx, idler)
}

func (r *Reconciler) ensureIdling(ctx context.Context, idler *toolchainv1alpha1.Idler) error {
	// Get all pods running in the namespace
	podList := &corev1.PodList{}
	if err := r.AllNamespacesClient.List(ctx, podList, client.InNamespace(idler.Name)); err != nil {
		return err
	}
	newStatusPods := make([]toolchainv1alpha1.Pod, 0, 10)
	for _, pod := range podList.Items {
		pod := pod // TODO We won't need it after upgrading to go 1.22: https://go.dev/blog/loopvar-preview
		podLogger := log.FromContext(ctx).WithValues("pod_name", pod.Name, "pod_phase", pod.Status.Phase)
		podCtx := log.IntoContext(ctx, podLogger)
		if trackedPod := findPodByName(idler, pod.Name); trackedPod != nil {
			// check the restart count for the trackedPod
			restartCount := getHighestRestartCount(pod.Status)
			if restartCount > RestartThreshold {
				podLogger.Info("Pod is restarting too often. Killing the pod", "restart_count", restartCount)
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				err := deletePodsAndCreateNotification(podCtx, pod, r, idler)
				if err != nil {
					return err
				}
				continue
			}
			timeoutSeconds := idler.Spec.TimeoutSeconds
			if isOwnedByVM(pod.ObjectMeta) {
				// use 1/12th of the timeout for VMs
				timeoutSeconds = timeoutSeconds / 12
			}
			// Already tracking this pod. Check the timeout.
			if time.Now().After(trackedPod.StartTime.Add(time.Duration(timeoutSeconds) * time.Second)) {
				podLogger.Info("Pod running for too long. Killing the pod.", "start_time", trackedPod.StartTime.Format("2006-01-02T15:04:05Z"), "timeout_seconds", timeoutSeconds)
				// Check if it belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
				err := deletePodsAndCreateNotification(podCtx, pod, r, idler)
				if err != nil {
					return err
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

	return r.updateStatusPods(ctx, idler, newStatusPods)
}

// Check if the pod belongs to a controller (Deployment, DeploymentConfig, etc) and scale it down to zero.
// if it is a standalone pod, delete it.
// Send notification if the deleted pod was managed by a controller, was a standalone pod that was not completed or was crashlooping
func deletePodsAndCreateNotification(podCtx context.Context, pod corev1.Pod, r *Reconciler, idler *toolchainv1alpha1.Idler) error {
	logger := log.FromContext(podCtx)
	var podReason string
	podCondition := pod.Status.Conditions
	for _, podCond := range podCondition {
		if podCond.Type == "Ready" {
			podReason = podCond.Reason
		}
	}
	appType, appName, deletedByController, err := r.scaleControllerToZero(podCtx, pod.ObjectMeta)
	if err != nil {
		return err
	}
	if !deletedByController { // Pod not managed by a controller. We can just delete the pod.
		logger.Info("Deleting pod without controller")
		if err := r.AllNamespacesClient.Delete(podCtx, &pod); err != nil {
			return err
		}
		logger.Info("Pod deleted")
	}
	if appName == "" {
		appName = pod.Name
		appType = "Pod"
	}

	// If a build pod is in "PodCompleted" status then it was not running so there's no reason to send an idler notification
	if podReason != "PodCompleted" || deletedByController {
		// By now either a pod has been deleted or scaled to zero by controller, idler Triggered notification should be sent
		if err := r.createNotification(podCtx, idler, appName, appType); err != nil {
			logger.Error(err, "failed to create Notification")
			if err = r.setStatusIdlerNotificationCreationFailed(podCtx, idler, err.Error()); err != nil {
				logger.Error(err, "failed to set status IdlerNotificationCreationFailed")
			} // not returning error to continue tracking remaining pods
		}
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

func (r *Reconciler) createNotification(ctx context.Context, idler *toolchainv1alpha1.Idler, appName string, appType string) error {
	log.FromContext(ctx).Info("Create Notification")
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
		if !errors.IsNotFound(err) {
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
				return errs.Wrapf(err, "unable to create Notification CR from Idler")
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
				return emails, errs.Wrapf(err, "could not get the MUR")
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

// scaleControllerToZero checks if the object has an owner controller (Deployment, ReplicaSet, etc)
// and scales the owner down to zero and returns "true".
// Otherwise, returns "false".
// It also returns the parent controller type and name or empty strings if there is no parent controller.
func (r *Reconciler) scaleControllerToZero(ctx context.Context, meta metav1.ObjectMeta) (string, string, bool, error) {
	log.FromContext(ctx).Info("Scaling controller to zero", "name", meta.Name)
	owners := meta.GetOwnerReferences()
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			switch owner.Kind {
			case "Deployment":
				return r.scaleDeploymentToZero(ctx, meta.Namespace, owner)
			case "ReplicaSet":
				return r.scaleReplicaSetToZero(ctx, meta.Namespace, owner)
			case "DaemonSet":
				return r.deleteDaemonSet(ctx, meta.Namespace, owner) // Nothing to scale down. Delete instead.
			case "StatefulSet":
				return r.scaleStatefulSetToZero(ctx, meta.Namespace, owner)
			case "DeploymentConfig":
				return r.scaleDeploymentConfigToZero(ctx, meta.Namespace, owner)
			case "ReplicationController":
				return r.scaleReplicationControllerToZero(ctx, meta.Namespace, owner)
			case "Job":
				return r.deleteJob(ctx, meta.Namespace, owner) // Nothing to scale down. Delete instead.
			case "VirtualMachineInstance":
				return r.stopVirtualMachine(ctx, meta.Namespace, owner) // Nothing to scale down. Stop instead.
			}
		}
	}
	return "", "", false, nil
}

func (r *Reconciler) scaleDeploymentToZero(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	logger := log.FromContext(ctx)
	logger.Info("Scaling deployment to zero", "name", owner.Name)
	d := &appsv1.Deployment{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, d); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	zero := int32(0)

	for _, deploymentOwner := range d.OwnerReferences {
		if supportedScaleResource := getSupportedScaleResource(deploymentOwner); supportedScaleResource != nil {
			// check for owner with scale sub resource
			if scaleResource, err := r.ScalesClient.Scales(d.Namespace).Get(ctx, supportedScaleResource.GroupResource(), deploymentOwner.Name, metav1.GetOptions{}); err == nil {
				scaleResource.Spec.Replicas = zero
				_, err = r.ScalesClient.Scales(d.Namespace).Update(ctx, supportedScaleResource.GroupResource(), scaleResource, metav1.UpdateOptions{})

				if err == nil {
					logger.Info("Deployment scaled to zero using scale sub resource", "name", d.Name)
					return owner.Kind, owner.Name, true, nil
				}

				return "", "", false, err
			} else if errors.IsInternalError(err) { // Internal error indicates that the specReplicasPath is not set on the custom resource - just update the scale resource
				scale := autoscalingv1.Scale{
					ObjectMeta: ctrl.ObjectMeta{
						Name:      deploymentOwner.Name,
						Namespace: d.Namespace,
					},
					Spec: autoscalingv1.ScaleSpec{
						Replicas: zero,
					},
				}
				_, err = r.ScalesClient.Scales(d.Namespace).Update(ctx, supportedScaleResource.GroupResource(), &scale, metav1.UpdateOptions{})

				if err == nil {
					logger.Info("Deployment scaled to zero using scale sub resource", "name", d.Name)
					return owner.Kind, owner.Name, true, nil
				}

				return "", "", false, err
			} else if !errors.IsNotFound(err) {
				return "", "", false, err
			}
		}
	}

	d.Spec.Replicas = &zero
	if err := r.AllNamespacesClient.Update(ctx, d); err != nil {
		return "", "", false, err
	}
	logger.Info("Deployment scaled to zero", "name", d.Name)
	return owner.Kind, owner.Name, true, nil
}

func getSupportedScaleResource(ownerReference metav1.OwnerReference) *schema.GroupVersionResource {
	if ownerGVK, err := schema.ParseGroupVersion(ownerReference.APIVersion); err == nil {
		for groupVersionKind, groupVersionResource := range SupportedScaleResources {
			if groupVersionKind.String() == ownerGVK.WithKind(ownerReference.Kind).String() {
				return &groupVersionResource
			}
		}
	}

	return nil
}

func (r *Reconciler) scaleReplicaSetToZero(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	logger := log.FromContext(ctx)
	logger.Info("Scaling replica set to zero", "name", owner.Name)
	rs := &appsv1.ReplicaSet{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, rs); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			logger.Info("replica set is not found; ignoring: it might be already deleted")
			return owner.Kind, owner.Name, true, nil
		}
		logger.Error(err, "error deleting rs")
		return "", "", false, err
	}

	appType, appName, deletedByController, err := r.scaleControllerToZero(ctx, rs.ObjectMeta) // Check if the ReplicaSet has another controller which owns it (i.g. Deployment)
	if err != nil {
		return "", "", false, err
	}
	if !deletedByController {
		// There is no controller that owns the ReplicaSet. Scale the ReplicaSet to zero.
		zero := int32(0)
		rs.Spec.Replicas = &zero
		if err := r.AllNamespacesClient.Update(ctx, rs); err != nil {
			return "", "", false, err
		}
		logger.Info("ReplicaSet scaled to zero", "name", rs.Name)
		appType = owner.Kind
		appName = owner.Name
	}
	return appType, appName, true, nil
}

func (r *Reconciler) deleteDaemonSet(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	ds := &appsv1.DaemonSet{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	if err := r.AllNamespacesClient.Delete(ctx, ds); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	log.FromContext(ctx).Info("DaemonSet deleted", "name", ds.Name)
	return owner.Kind, owner.Name, true, nil
}

func (r *Reconciler) scaleStatefulSetToZero(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	s := &appsv1.StatefulSet{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, s); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	zero := int32(0)
	s.Spec.Replicas = &zero
	if err := r.AllNamespacesClient.Update(ctx, s); err != nil {
		return "", "", false, err
	}
	log.FromContext(ctx).Info("StatefulSet scaled to zero", "name", s.Name)
	return owner.Kind, owner.Name, true, nil
}

func (r *Reconciler) scaleDeploymentConfigToZero(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	dc := &openshiftappsv1.DeploymentConfig{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, dc); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	dc.Spec.Replicas = 0
	dc.Spec.Paused = false
	if err := r.AllNamespacesClient.Update(ctx, dc); err != nil {
		return "", "", false, err
	}
	log.FromContext(ctx).Info("DeploymentConfig scaled to zero", "name", dc.Name)
	return owner.Kind, owner.Name, true, nil
}

func (r *Reconciler) scaleReplicationControllerToZero(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	rc := &corev1.ReplicationController{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, rc); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	appType, appName, deletedByController, err := r.scaleControllerToZero(ctx, rc.ObjectMeta) // Check if the ReplicationController has another controller which owns it (i.g. DeploymentConfig)
	if err != nil {
		return "", "", false, err
	}
	if !deletedByController {
		// There is no controller who owns the ReplicationController. Scale the ReplicationController to zero.
		zero := int32(0)
		rc.Spec.Replicas = &zero
		if err := r.AllNamespacesClient.Update(ctx, rc); err != nil {
			return "", "", false, err
		}
		log.FromContext(ctx).Info("ReplicationController scaled to zero", "name", rc.Name)
		appType = owner.Kind
		appName = owner.Name
	}
	return appType, appName, true, nil
}

func (r *Reconciler) deleteJob(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	logger := log.FromContext(ctx)
	j := &batchv1.Job{}
	if err := r.AllNamespacesClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, j); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			logger.Info("job not found")
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	// see https://github.com/kubernetes/kubernetes/issues/20902#issuecomment-321484735
	// also, this may be needed for the e2e tests if the call to `client.Delete` comes too quickly after creating the job,
	// which may leave the job's pod running but orphan, hence causing a test failure (and making the test flaky)
	propagationPolicy := metav1.DeletePropagationBackground

	if err := r.AllNamespacesClient.Delete(ctx, j, &client.DeleteOptions{
		PropagationPolicy: &propagationPolicy,
	}); err != nil {
		if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
			return owner.Kind, owner.Name, true, nil
		}
		return "", "", false, err
	}
	logger.Info("Job deleted", "name", j.Name)
	return owner.Kind, owner.Name, true, nil
}

func (r *Reconciler) stopVirtualMachine(ctx context.Context, namespace string, owner metav1.OwnerReference) (string, string, bool, error) {
	logger := log.FromContext(ctx)
	// get the virtualmachineinstance info from the owner reference
	vmInstance, err := r.DynamicClient.Resource(vmInstanceGVR).Namespace(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
		logger.Info("VirtualMachineInstance not found", "name", owner.Name)
		return owner.Kind, owner.Name, true, nil
	}
	if err != nil {
		return "", "", false, err
	}

	// get virtualmachineinstance owner reference (virtualmachine)
	vmInstanceOwners := vmInstance.GetOwnerReferences()
	vmiOwnerIndex := -1
	for i, vmInstanceOwner := range vmInstanceOwners {
		if vmInstanceOwner.Controller != nil && *vmInstanceOwner.Controller && vmInstanceOwner.Kind == "VirtualMachine" {
			vmiOwnerIndex = i
			break
		}
	}

	if vmiOwnerIndex == -1 {
		return "", "", false, fmt.Errorf("VirtualMachineInstance '%s' is missing a VirtualMachine owner reference", vmInstance.GetName())
	}

	// get the virtualmachine resource
	vm, err := r.DynamicClient.Resource(vmGVR).Namespace(namespace).Get(ctx, vmInstanceOwners[vmiOwnerIndex].Name, metav1.GetOptions{})
	if errors.IsNotFound(err) { // Ignore not found errors. Can happen if the parent controller has been deleted. The Garbage Collector should delete the pods shortly.
		logger.Info("VirtualMachine not found")
		return owner.Kind, owner.Name, true, nil
	}
	if err != nil {
		return "", "", false, err
	}

	// patch the virtualmachine resource by setting spec.running to false in order to stop the VM
	patch := []byte(`{"spec":{"running":false}}`)
	_, err = r.DynamicClient.Resource(vmGVR).Namespace(namespace).Patch(ctx, vm.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", "", false, err
	}

	logger.Info("VirtualMachine stopped", "name", vm.GetName())
	return vm.GetKind(), vm.GetName(), true, nil
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
func nextPodToBeKilledAfter(idler *toolchainv1alpha1.Idler) *time.Duration {
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
	return &d
}

// findShortestRequeueDuration finds the shortest duration the given durations
// returns the shortest duration to requeue after for idler
func findShortestRequeueDuration(idler *toolchainv1alpha1.Idler) time.Duration {
	durations := make([]*time.Duration, 0, 3)
	nextPodToKillAfter := nextPodToBeKilledAfter(idler)
	maxRequeueDuration := RequeueTimeThreshold
	idlerTimeoutDuration := time.Duration(idler.Spec.TimeoutSeconds) * time.Second
	durations = append(durations, nextPodToKillAfter, &maxRequeueDuration, &idlerTimeoutDuration)
	var shortest *time.Duration
	for _, d := range durations {
		if d != nil {
			if shortest == nil || *d < *shortest {
				shortest = d
			}
		}
	}
	return *shortest
}

// updateStatusPods updates the status pods to the new ones but only if something changed. Order is ignored.
func (r *Reconciler) updateStatusPods(ctx context.Context, idler *toolchainv1alpha1.Idler, newPods []toolchainv1alpha1.Pod) error {
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
	return r.Client.Status().Update(ctx, idler)
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
	return errs.Wrapf(err, format, args...)
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
