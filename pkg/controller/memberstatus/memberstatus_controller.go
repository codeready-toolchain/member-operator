package memberstatus

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	crtCfg "github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/version"

	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/status"

	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	errs "github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_memberstatus")

// general memberstatus constants
const (
	defaultRequeueTime = time.Second * 5
)

// custom component condition error messages & reasons
const (
// // deployment ready
// reasonDeploymentReady = "DeploymentReady"

// // deployment not found
// reasonNoDeployment = "DeploymentNotFound"

// // deployment not ready
// reasonDeploymentConditionNotReady = "DeploymentNotReady"

// // kubefed not found
// reasonHostConnectionNotFound = "KubefedNotFound"

// // kubefed last probe time exceeded
// reasonHostConnectionLastProbeTimeExceeded = "KubefedLastProbeTimeExceeded"
)

// statusComponentTags are used in the overall condition to point out which components are not ready
type statusComponentTag string

const (
	memberOperator statusComponentTag = "memberOperator"
	hostConnection statusComponentTag = "hostConnection"
)

// Add creates a new MemberStatus Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, crtConfig *crtCfg.Config) error {
	return add(mgr, newReconciler(mgr, crtConfig))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, crtConfig *crtCfg.Config) *ReconcileMemberStatus {
	return &ReconcileMemberStatus{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		getHostCluster: cluster.GetHostCluster,
		config:         crtConfig,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r *ReconcileMemberStatus) error {
	// create a new controller
	c, err := controller.New("memberstatus-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// watch for changes to primary resource MemberStatus
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.MemberStatus{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileMemberStatus implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileMemberStatus{}

// ReconcileMemberStatus reconciles a MemberStatus object
type ReconcileMemberStatus struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client         client.Client
	scheme         *runtime.Scheme
	getHostCluster func() (*cluster.FedCluster, bool)
	config         *crtCfg.Config
}

// Reconcile reads the state of toolchain member cluster components and updates the MemberStatus resource with information useful for observation or troubleshooting
func (r *ReconcileMemberStatus) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling MemberStatus")

	// fetch the MemberStatus
	memberStatus := &toolchainv1alpha1.MemberStatus{}
	err := r.client.Get(context.TODO(), request.NamespacedName, memberStatus)

	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return reconcile.Result{}, nil
		}
		reqLogger.Error(err, "Unable to fetch MemberStatus resource")
		return reconcile.Result{}, err
	}

	err = r.aggregateAndUpdateStatus(reqLogger, memberStatus)
	if err != nil {
		reqLogger.Error(err, "Failed to update status")
		return reconcile.Result{RequeueAfter: defaultRequeueTime}, err
	}

	reqLogger.Info(fmt.Sprintf("Finished updating MemberStatus, requeueing after %v", defaultRequeueTime))
	return reconcile.Result{RequeueAfter: defaultRequeueTime}, nil
}

type statusHandler struct {
	name         statusComponentTag
	handleStatus func(logger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error
}

// aggregateAndUpdateStatus runs each of the status handlers. Each status handler reports readiness for a toolchain component. If any
// component status is not ready then it will set the condition of the top-level status of the MemberStatus resource to not ready.
func (r *ReconcileMemberStatus) aggregateAndUpdateStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {

	memberOperatorStatusHandler := statusHandler{name: memberOperator, handleStatus: r.memberOperatorHandleStatus}
	hostConnectionStatusHandler := statusHandler{name: hostConnection, handleStatus: r.hostConnectionHandleStatus}

	statusHandlers := []statusHandler{memberOperatorStatusHandler, hostConnectionStatusHandler}

	// track components that are not ready
	unreadyComponents := []string{}

	// retrieve component statuses eg. kubefed, member deployment
	for _, handler := range statusHandlers {
		err := handler.handleStatus(reqLogger, memberStatus)
		if err != nil {
			reqLogger.Error(err, "status update problem")
			unreadyComponents = append(unreadyComponents, string(handler.name))
		}
	}

	// if any components were not ready then set the overall status to not ready
	if len(unreadyComponents) > 0 {
		return r.setStatusNotReady(memberStatus, fmt.Sprintf("components not ready: %v", unreadyComponents))
	}
	return r.setStatusReady(memberStatus)
}

// hostConnectionHandleStatus retrieves the host cluster object that represents the connection between this member cluster and the host cluster.
// It then takes the status from the cluster object and adds it to MemberStatus. Finally, it checks its status and will return an error if
// its status is not ready
func (r *ReconcileMemberStatus) hostConnectionHandleStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {

	attributes := status.KubefedAttributes{
		GetClusterFunc: r.getHostCluster,
		Period:         r.config.GetClusterHealthCheckPeriod(),
		Timeout:        r.config.GetClusterHealthCheckTimeout(),
		Threshold:      r.config.GetClusterHealthCheckFailureThreshold(),
	}

	// look up host connection status
	clusterStatus, err := status.GetKubefedConditions(attributes)
	memberStatus.Status.HostConnection = clusterStatus

	return err
}

// memberOperatorHandleStatus retrieves the Deployment for the member operator and adds its status to MemberStatus. It returns an error
// if any of the conditions have a status that is not 'true'
func (r *ReconcileMemberStatus) memberOperatorHandleStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {
	operatorStatus := &toolchainv1alpha1.MemberOperatorStatus{
		Version:        version.Version,
		Revision:       version.Commit,
		BuildTimestamp: version.BuildTime,
	}

	// look up status of member deployment
	memberOperatorDeploymentName, err := k8sutil.GetOperatorName()
	if err != nil {
		err = errs.Wrap(err, status.ErrMsgCannotGetDeployment)
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusReasonDeploymentNotFound, err.Error())
		operatorStatus.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		memberStatus.Status.MemberOperator = operatorStatus
		return err
	}
	operatorStatus.Deployment.Name = memberOperatorDeploymentName

	// check member operator deployment status
	deploymentConditions, err := status.GetDeploymentStatusConditions(r.client, memberOperatorDeploymentName, memberStatus.Namespace)

	// update memberstatus
	operatorStatus.Conditions = deploymentConditions
	memberStatus.Status.MemberOperator = operatorStatus
	return err
}

// updateStatusConditions updates Member status conditions with the new conditions
func (r *ReconcileMemberStatus) updateStatusConditions(memberStatus *toolchainv1alpha1.MemberStatus, newConditions ...toolchainv1alpha1.Condition) error {
	// the controller should always update at least the last updated timestamp of the status so the status should be updated regardless of whether
	// any specific fields were updated. This way a problem with the controller can be indicated if the last updated timestamp was not updated.
	conditionsWithTimestamps := []toolchainv1alpha1.Condition{}
	for _, condition := range newConditions {
		condition.LastTransitionTime = metav1.Now()
		condition.LastUpdatedTime = &metav1.Time{Time: condition.LastTransitionTime.Time}
		conditionsWithTimestamps = append(conditionsWithTimestamps, condition)
	}
	memberStatus.Status.Conditions = conditionsWithTimestamps
	return r.client.Status().Update(context.TODO(), memberStatus)
}

func (r *ReconcileMemberStatus) setStatusReady(memberStatus *toolchainv1alpha1.MemberStatus) error {
	return r.updateStatusConditions(
		memberStatus,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.ToolchainStatusReasonAllComponentsReady,
		})
}

func (r *ReconcileMemberStatus) setStatusNotReady(memberStatus *toolchainv1alpha1.MemberStatus, message string) error {
	return r.updateStatusConditions(
		memberStatus,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.ToolchainStatusReasonComponentsNotReady,
			Message: message,
		})
}
