package memberstatus

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	crtCfg "github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/version"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/status"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
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

// statusComponentTags are used in the overall condition to point out which components are not ready
type statusComponentTag string

const (
	memberOperatorTag statusComponentTag = "memberOperator"
	hostConnectionTag statusComponentTag = "hostConnection"
	resourceUsageTag  statusComponentTag = "resourceUsage"
	routesTag         statusComponentTag = "routes"
	cheTag            statusComponentTag = "che"

	labelNodeRoleMaster = "node-role.kubernetes.io/master"
	labelNodeRoleWorker = "node-role.kubernetes.io/worker"
)

// Add creates a new MemberStatus Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, crtConfig *crtCfg.Config, allNamespacesClient client.Client) error {
	return add(mgr, newReconciler(mgr, crtConfig, allNamespacesClient))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, crtConfig *crtCfg.Config, allNamespacesClient client.Client) *ReconcileMemberStatus {
	return &ReconcileMemberStatus{
		client:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		getHostCluster:      cluster.GetHostCluster,
		config:              crtConfig,
		allNamespacesClient: allNamespacesClient,
		cheClient:           che.DefaultClient,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r *ReconcileMemberStatus) error {
	// Create a new controller
	c, err := controller.New("memberstatus-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource MemberStatus
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
	client              client.Client
	scheme              *runtime.Scheme
	getHostCluster      func() (*cluster.CachedToolchainCluster, bool)
	config              *crtCfg.Config
	allNamespacesClient client.Client
	cheClient           *che.Client
}

// Reconcile reads the state of toolchain member cluster components and updates the MemberStatus resource with information useful for observation or troubleshooting
func (r *ReconcileMemberStatus) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling MemberStatus")
	requeueTime := r.config.GetMemberStatusRefreshTime()

	// Fetch the MemberStatus
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
		return reconcile.Result{RequeueAfter: requeueTime}, err
	}

	reqLogger.Info(fmt.Sprintf("Finished updating MemberStatus, requeueing after %v", requeueTime))
	return reconcile.Result{RequeueAfter: requeueTime}, nil
}

type statusHandler struct {
	name         statusComponentTag
	handleStatus func(logger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error
}

// aggregateAndUpdateStatus runs each of the status handlers. Each status handler reports readiness for a toolchain component. If any
// component status is not ready then it will set the condition of the top-level status of the MemberStatus resource to not ready.
func (r *ReconcileMemberStatus) aggregateAndUpdateStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {

	statusHandlers := []statusHandler{
		{name: memberOperatorTag, handleStatus: r.memberOperatorHandleStatus},
		{name: hostConnectionTag, handleStatus: r.hostConnectionHandleStatus},
		{name: resourceUsageTag, handleStatus: r.loadCurrentResourceUsage},
		{name: routesTag, handleStatus: r.routesHandleStatus},
		{name: cheTag, handleStatus: r.cheHandleStatus},
	}

	// Track components that are not ready
	var unreadyComponents []string

	// Retrieve component statuses eg. toolchainCluster, member deployment
	for _, statusHandler := range statusHandlers {
		err := statusHandler.handleStatus(reqLogger, memberStatus)
		if err != nil {
			reqLogger.Error(err, "status update problem")
			unreadyComponents = append(unreadyComponents, string(statusHandler.name))
		}
	}

	// If any components were not ready then set the overall status to not ready
	if len(unreadyComponents) > 0 {
		return r.setStatusNotReady(memberStatus, fmt.Sprintf("components not ready: %v", unreadyComponents))
	}
	return r.setStatusReady(memberStatus)
}

// hostConnectionHandleStatus retrieves the host cluster object that represents the connection between this member cluster and the host cluster.
// It then takes the status from the cluster object and adds it to MemberStatus. Finally, it checks its status and will return an error if
// its status is not ready
func (r *ReconcileMemberStatus) hostConnectionHandleStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {

	attributes := status.ToolchainClusterAttributes{
		GetClusterFunc: r.getHostCluster,
		Period:         r.config.GetClusterHealthCheckPeriod(),
		Timeout:        r.config.GetToolchainClusterTimeout(),
	}

	// look up host connection status
	connectionConditions := status.GetToolchainClusterConditions(reqLogger, attributes)
	err := status.ValidateComponentConditionReady(connectionConditions...)
	memberStatus.Status.Host = &toolchainv1alpha1.HostStatus{
		Conditions: connectionConditions,
	}

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

	// Look up status of member deployment
	memberOperatorDeploymentName, err := k8sutil.GetOperatorName()
	if err != nil {
		err = errs.Wrap(err, status.ErrMsgCannotGetDeployment)
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusDeploymentNotFoundReason, err.Error())
		operatorStatus.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		memberStatus.Status.MemberOperator = operatorStatus
		return err
	}
	operatorStatus.DeploymentName = memberOperatorDeploymentName

	// Check member operator deployment status
	deploymentConditions := status.GetDeploymentStatusConditions(r.client, memberOperatorDeploymentName, memberStatus.Namespace)
	err = status.ValidateComponentConditionReady(deploymentConditions...)

	// Update memberstatus
	operatorStatus.Conditions = deploymentConditions
	memberStatus.Status.MemberOperator = operatorStatus
	return err
}

// loadCurrentResourceUsage loads the current usage of the cluster and stores it into the member status
func (r *ReconcileMemberStatus) loadCurrentResourceUsage(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {
	memberStatus.Status.ResourceUsage.MemoryUsagePerNodeRole = map[string]int{}
	allocatableValues, err := r.getAllocatableValues(reqLogger)
	if err != nil {
		return err
	}

	nodeMetricsList := &v1beta1.NodeMetricsList{}
	if err := r.client.List(context.TODO(), nodeMetricsList); err != nil {
		return err
	}

	usagePerRole := map[string]float32{}
	allocatablePerRole := map[string]float32{}
	for _, nodeMetric := range nodeMetricsList.Items {
		if memoryUsage, usageFound := nodeMetric.Usage["memory"]; usageFound {
			if nodeInfo, nodeFound := allocatableValues[nodeMetric.Name]; nodeFound {

				for _, role := range nodeInfo.roles {
					// let's do the sum of usages and the allocatable capacity for each of the node roles
					usagePerRole[role] += float32(memoryUsage.Value())
					allocatablePerRole[role] += float32(nodeInfo.allocatable.Value())
				}

				// let's remove the used allocatable value from the map so we can later check if all values were used
				delete(allocatableValues, nodeMetric.Name)
			} else {
				reqLogger.Info("skipping NodeMetrics resource - there wasn't found corresponding node that would be monitored", "name", nodeMetric.Name)
			}
			continue
		}
		return fmt.Errorf("memory item not found in NodeMetrics: %v", nodeMetric)
	}

	// Check if all allocatable values were used or if there is some value that the NodeMetrics resource wasn't found for.
	// In such a case we need to return an error, because the metrics are not complete
	if len(allocatableValues) > 0 {
		var nodeNames []string
		for nodeName := range allocatableValues {
			nodeNames = append(nodeNames, nodeName)
		}
		return fmt.Errorf("missing NodeMetrics resource for Nodes: %v", nodeNames)
	}

	// usagePerRole contains sum of usages per node role and
	// allocatablePerRole contains sum of allocatable capacity per node role
	// thus we can now count the ration and store the percentage of the usage
	for role, usage := range usagePerRole {
		memberStatus.Status.ResourceUsage.MemoryUsagePerNodeRole[role] = int((usage / allocatablePerRole[role]) * 100)
	}
	return nil
}

// routesHandleStatus retrieves the public routes which should be exposed to the users. Such as Web Console and Che Dashboard URLs.
// Returns an error if at least one of the required routes are not available.
func (r *ReconcileMemberStatus) routesHandleStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {
	if memberStatus.Status.Routes == nil {
		memberStatus.Status.Routes = &toolchainv1alpha1.Routes{}
	}
	consoleURL, err := r.consoleURL()
	if err != nil {
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusConsoleRouteUnavailableReason, err.Error())
		memberStatus.Status.Routes.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		return err
	}
	memberStatus.Status.Routes.ConsoleURL = consoleURL

	cheURL, err := r.cheDashboardURL()
	if err != nil {
		if r.config.IsCheRequired() {
			errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheRouteUnavailableReason, err.Error())
			memberStatus.Status.Routes.Conditions = []toolchainv1alpha1.Condition{*errCondition}
			return err
		}
		reqLogger.Info("Che route is not available but not required. Ignoring.", "err", err.Error())
	}
	memberStatus.Status.Routes.CheDashboardURL = cheURL

	readyCondition := status.NewComponentReadyCondition(toolchainv1alpha1.ToolchainStatusMemberStatusRoutesAvailableReason)
	memberStatus.Status.Routes.Conditions = []toolchainv1alpha1.Condition{*readyCondition}

	return nil
}

// cheHandleStatus checks all necessary aspects related integration between the member operator and Che
// Returns an error if any problems are discovered.
func (r *ReconcileMemberStatus) cheHandleStatus(reqLogger logr.Logger, memberStatus *toolchainv1alpha1.MemberStatus) error {
	if memberStatus.Status.Che == nil {
		memberStatus.Status.Che = &toolchainv1alpha1.CheStatus{}
	}

	// Is che user deletion enabled
	if !r.config.IsCheUserDeletionEnabled() {
		// Che user deletion is not enabled, set condition to Ready. No further checks required
		readyCondition := status.NewComponentReadyCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheUserDeletionNotEnabledReason)
		memberStatus.Status.Che.Conditions = []toolchainv1alpha1.Condition{*readyCondition}
		return nil
	}

	if !r.isCheAdminUserConfigured() {
		err := fmt.Errorf("Che admin user credentials are not configured but Che user deletion is enabled")
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheAdminUserNotConfiguredReason, err.Error())
		memberStatus.Status.Che.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		return err
	}

	// Get che route for testing user API
	if _, err := r.cheDashboardURL(); err != nil {
		wrappedErr := errs.Wrapf(err, "Che dashboard URL unavailable but Che user deletion is enabled")
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheRouteUnavailableReason, wrappedErr.Error())
		memberStatus.Status.Che.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		return err
	}

	// User API check
	if err := r.cheClient.UserAPICheck(); err != nil {
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheUserAPICheckFailedReason, err.Error())
		memberStatus.Status.Che.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		return err
	}

	readyCondition := status.NewComponentReadyCondition(toolchainv1alpha1.ToolchainStatusMemberStatusCheReadyReason)
	memberStatus.Status.Che.Conditions = []toolchainv1alpha1.Condition{*readyCondition}
	return nil
}

func (r *ReconcileMemberStatus) getAllocatableValues(reqLogger logr.Logger) (map[string]nodeInfo, error) {
	nodes := &corev1.NodeList{}
	err := r.client.List(context.TODO(), nodes)
	if err != nil {
		return nil, errs.Wrapf(err, "unable to list Nodes")
	}
	allocatableValues := map[string]nodeInfo{}
	for _, node := range nodes.Items {
		roles := getNodeRoles(node)
		if len(roles) == 0 {
			reqLogger.Info("The node doesn't have role worker nor master - is ignored in resource consumption computing", "nodeName", node.Name)
			continue
		}
		if memoryCapacity, found := node.Status.Allocatable["memory"]; found {
			allocatableValues[node.Name] = nodeInfo{
				roles:        roles,
				allocatable: memoryCapacity,
			}
		}
	}
	return allocatableValues, nil
}

type nodeInfo struct {
	roles       []string
	allocatable resource.Quantity
}

// getNodeRoles returns an array containing the roles (i.e. worker, master) fulfilled by the specified node
func getNodeRoles(node corev1.Node) (roles []string) {
	if _, isWorker := node.Labels[labelNodeRoleWorker]; isWorker {
		roles = append(roles, "worker")
	}

	if _, isMaster := node.Labels[labelNodeRoleMaster]; isMaster {
		roles = append(roles, "master")
	}
	return
}

// updateStatusConditions updates Member status conditions with the new conditions
func (r *ReconcileMemberStatus) updateStatusConditions(memberStatus *toolchainv1alpha1.MemberStatus, newConditions ...toolchainv1alpha1.Condition) error {
	// the controller should always update at least the last updated timestamp of the status so the status should be updated regardless of whether
	// any specific fields were updated. This way a problem with the controller can be indicated if the last updated timestamp was not updated.
	var conditionsWithTimestamps []toolchainv1alpha1.Condition
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
			Reason: toolchainv1alpha1.ToolchainStatusAllComponentsReadyReason,
		})
}

func (r *ReconcileMemberStatus) setStatusNotReady(memberStatus *toolchainv1alpha1.MemberStatus, message string) error {
	return r.updateStatusConditions(
		memberStatus,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.ToolchainStatusComponentsNotReadyReason,
			Message: message,
		})
}

func (r *ReconcileMemberStatus) consoleURL() (string, error) {
	route := &routev1.Route{}
	namespacedName := types.NamespacedName{Namespace: r.config.GetConsoleNamespace(), Name: r.config.GetConsoleRouteName()}
	if err := r.allNamespacesClient.Get(context.TODO(), namespacedName, route); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s/%s", route.Spec.Host, route.Spec.Path), nil
}

func (r *ReconcileMemberStatus) cheDashboardURL() (string, error) {
	route := &routev1.Route{}
	namespacedName := types.NamespacedName{Namespace: r.config.GetCheNamespace(), Name: r.config.GetCheRouteName()}
	err := r.allNamespacesClient.Get(context.TODO(), namespacedName, route)
	if err != nil {
		return "", err
	}
	scheme := "https"
	if route.Spec.TLS == nil || *route.Spec.TLS == (routev1.TLSConfig{}) {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, route.Spec.Host, route.Spec.Path), nil
}

// isCheAdminUserConfigured returns true if the Che admin username and password are both set and not empty.
// Returns false otherwise.
func (r *ReconcileMemberStatus) isCheAdminUserConfigured() bool {
	return r.config.GetCheAdminUsername() != "" && r.config.GetCheAdminPassword() != ""
}
