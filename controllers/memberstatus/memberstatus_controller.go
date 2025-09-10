package memberstatus

import (
	"context"
	"fmt"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/version"
	commonclient "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	membercfg "github.com/codeready-toolchain/toolchain-common/pkg/configuration/memberoperatorconfig"
	"github.com/codeready-toolchain/toolchain-common/pkg/status"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/log"

	routev1 "github.com/openshift/api/route/v1"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	metrics "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// statusComponentTags are used in the overall condition to point out which components are not ready
type statusComponentTag string

const (
	MemberStatusName = "toolchain-member-status"

	memberOperatorTag statusComponentTag = "memberOperator"
	hostConnectionTag statusComponentTag = "hostConnection"
	resourceUsageTag  statusComponentTag = "resourceUsage"
	routesTag         statusComponentTag = "routes"

	labelNodeRoleMaster          = "node-role.kubernetes.io/master"
	labelNodeRoleWorker          = "node-role.kubernetes.io/worker"
	labelNodeRoleInfra           = "node-role.kubernetes.io/infra"
	memberOperatorRepoName       = "member-operator"
	memberOperatorRepoBranchName = "master"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.MemberStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconciler reconciles a MemberStatus object
type Reconciler struct {
	Client              client.Client
	Scheme              *runtime.Scheme
	GetHostCluster      func() (*cluster.CachedToolchainCluster, bool)
	AllNamespacesClient client.Client
	VersionCheckManager status.VersionCheckManager
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberstatuses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberstatuses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberstatuses/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups=metrics.k8s.io,resources=*,verbs=get;list;watch
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch

// Reconcile reads the state of toolchain member cluster components and updates the MemberStatus resource with information useful for observation or troubleshooting
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling MemberStatus")

	// retrieve the latest config and use it for this reconciliation
	config, err := membercfg.GetConfiguration(r.Client)
	if err != nil {
		return reconcile.Result{}, errs.Wrapf(err, "unable to get MemberOperatorConfig")
	}

	requeuePeriod := config.MemberStatus().RefreshPeriod()

	// Fetch the MemberStatus
	memberStatus := &toolchainv1alpha1.MemberStatus{}
	err = r.Client.Get(ctx, request.NamespacedName, memberStatus)

	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return reconcile.Result{}, nil
		}
		reqLogger.Error(err, "Unable to fetch MemberStatus resource")
		return reconcile.Result{}, err
	}

	err = r.aggregateAndUpdateStatus(ctx, memberStatus, config)
	if err != nil {
		reqLogger.Error(err, "Failed to update status")
		return reconcile.Result{RequeueAfter: requeuePeriod}, err
	}

	reqLogger.Info(fmt.Sprintf("Finished updating MemberStatus, requeueing after %v", requeuePeriod))
	return reconcile.Result{RequeueAfter: requeuePeriod}, nil
}

type statusHandler struct {
	name         statusComponentTag
	handleStatus func(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, config membercfg.Configuration) error
}

// aggregateAndUpdateStatus runs each of the status handlers. Each status handler reports readiness for a toolchain component. If any
// component status is not ready then it will set the condition of the top-level status of the MemberStatus resource to not ready.
func (r *Reconciler) aggregateAndUpdateStatus(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, config membercfg.Configuration) error {
	statusHandlers := []statusHandler{
		{name: memberOperatorTag, handleStatus: r.memberOperatorHandleStatus},
		{name: hostConnectionTag, handleStatus: r.hostConnectionHandleStatus},
		{name: resourceUsageTag, handleStatus: r.loadCurrentResourceUsage},
		{name: routesTag, handleStatus: r.routesHandleStatus},
	}

	// Track components that are not ready
	var unreadyComponents []string

	// Retrieve component statuses eg. toolchainCluster, member deployment
	for _, statusHandler := range statusHandlers {
		err := statusHandler.handleStatus(ctx, memberStatus, config)
		if err != nil {
			log.FromContext(ctx).Error(err, "status update problem")
			unreadyComponents = append(unreadyComponents, string(statusHandler.name))
		}
	}

	// If any components were not ready then set the overall status to not ready
	if len(unreadyComponents) > 0 {
		return r.setStatusNotReady(ctx, memberStatus, fmt.Sprintf("components not ready: %v", unreadyComponents))
	}
	return r.setStatusReady(ctx, memberStatus)
}

// hostConnectionHandleStatus retrieves the host cluster object that represents the connection between this member cluster and the host cluster.
// It then takes the status from the cluster object and adds it to MemberStatus. Finally, it checks its status and will return an error if
// its status is not ready
func (r *Reconciler) hostConnectionHandleStatus(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, config membercfg.Configuration) error {

	attributes := status.ToolchainClusterAttributes{
		GetClusterFunc: r.GetHostCluster,
		Period:         config.ToolchainCluster().HealthCheckPeriod(),
		Timeout:        config.ToolchainCluster().HealthCheckTimeout(),
	}

	// look up host connection status
	reqLogger := log.FromContext(ctx)
	connectionConditions := status.GetToolchainClusterConditions(reqLogger, attributes)
	err := status.ValidateComponentConditionReady(connectionConditions...)
	memberStatus.Status.Host = &toolchainv1alpha1.HostStatus{
		Conditions: connectionConditions,
	}

	return err
}

// memberOperatorHandleStatus retrieves the Deployment for the member operator and adds its status to MemberStatus. It returns an error
// if any of the conditions have a status that is not 'true'
func (r *Reconciler) memberOperatorHandleStatus(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, memberConfig membercfg.Configuration) error {
	// ensure member operator status is set
	if memberStatus.Status.MemberOperator == nil {
		memberStatus.Status.MemberOperator = &toolchainv1alpha1.MemberOperatorStatus{}
	}

	operatorStatus := &toolchainv1alpha1.MemberOperatorStatus{
		Version:        version.Version,
		Revision:       version.Commit,
		BuildTimestamp: version.BuildTime,
		RevisionCheck:  memberStatus.Status.MemberOperator.RevisionCheck, // let's copy the last revision check object if any
	}

	// Look up status of member deployment
	memberOperatorName, errDeploy := configuration.GetOperatorName()
	if errDeploy != nil {
		errDeploy = errs.Wrap(errDeploy, status.ErrMsgCannotGetDeployment)
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusDeploymentNotFoundReason, errDeploy.Error())
		operatorStatus.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		memberStatus.Status.MemberOperator = operatorStatus
		return errDeploy
	}
	memberOperatorDeploymentName := fmt.Sprintf("%s-controller-manager", memberOperatorName)
	operatorStatus.DeploymentName = memberOperatorDeploymentName

	// Check member operator deployment status
	deploymentConditions := status.GetDeploymentStatusConditions(ctx, r.Client, memberOperatorDeploymentName, memberStatus.Namespace)
	errDeploy = status.ValidateComponentConditionReady(deploymentConditions...)
	operatorStatus.Conditions = deploymentConditions
	memberStatus.Status.MemberOperator = operatorStatus

	isProd := isProdEnvironment(memberConfig)
	githubRepo := commonclient.GitHubRepository{
		Org:               toolchainv1alpha1.ProviderLabelValue,
		Name:              memberOperatorRepoName,
		Branch:            memberOperatorRepoBranchName,
		DeployedCommitSHA: version.Commit,
	}

	// verify deployment version
	versionCondition := r.VersionCheckManager.CheckDeployedVersionIsUpToDate(ctx, isProd, memberConfig.GitHubSecret().AccessTokenKey(), memberStatus.Status.MemberOperator.RevisionCheck.Conditions, githubRepo)
	errVersionCheck := status.ValidateComponentConditionReady(*versionCondition)
	memberStatus.Status.MemberOperator.RevisionCheck.Conditions = []toolchainv1alpha1.Condition{*versionCondition}
	if errVersionCheck != nil {
		return errVersionCheck // we can return
	}

	return errDeploy
}

func isProdEnvironment(memberConfig membercfg.Configuration) bool {
	return memberConfig.Environment() == "prod"
}

// loadCurrentResourceUsage loads the current usage of the cluster and stores it into the member status
func (r *Reconciler) loadCurrentResourceUsage(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, _ membercfg.Configuration) error {
	memberStatus.Status.ResourceUsage.MemoryUsagePerNodeRole = map[string]int{}
	allocatableValues, err := r.getAllocatableValues(ctx)
	if err != nil {
		return err
	}
	nodeMetricsList := &metrics.NodeMetricsList{}
	if err := r.Client.List(ctx, nodeMetricsList); err != nil {
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
			}
			continue
		}
		return fmt.Errorf("memory item not found in NodeMetrics: %v", nodeMetric)
	}

	// Check if all allocatable values were used or if there are more than one value that the NodeMetrics resource wasn't found for.
	// In such a case we need to return an error, because the metrics are not complete
	if len(allocatableValues) > 1 {
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

// routesHandleStatus retrieves the public routes which should be exposed to the users. Such as Web Console URL.
// Returns an error if at least one of the required routes are not available.
func (r *Reconciler) routesHandleStatus(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, config membercfg.Configuration) error {
	if memberStatus.Status.Routes == nil {
		memberStatus.Status.Routes = &toolchainv1alpha1.Routes{}
	}
	consoleURL, err := r.consoleURL(ctx, config)
	if err != nil {
		errCondition := status.NewComponentErrorCondition(toolchainv1alpha1.ToolchainStatusMemberStatusConsoleRouteUnavailableReason, err.Error())
		memberStatus.Status.Routes.Conditions = []toolchainv1alpha1.Condition{*errCondition}
		return err
	}
	memberStatus.Status.Routes.ConsoleURL = consoleURL

	readyCondition := status.NewComponentReadyCondition(toolchainv1alpha1.ToolchainStatusMemberStatusRoutesAvailableReason)
	memberStatus.Status.Routes.Conditions = []toolchainv1alpha1.Condition{*readyCondition}

	return nil
}

func (r *Reconciler) getAllocatableValues(ctx context.Context) (map[string]nodeInfo, error) {
	nodes := &corev1.NodeList{}
	err := r.Client.List(ctx, nodes)
	if err != nil {
		return nil, errs.Wrapf(err, "unable to list Nodes")
	}
	allocatableValues := map[string]nodeInfo{}
	for _, node := range nodes.Items {
		roles := getNodeRoles(node)
		if len(roles) == 0 {
			log.FromContext(ctx).Info("The node doesn't have role worker nor master - is ignored in resource consumption computing", "nodeName", node.Name)
			continue
		}
		if memoryCapacity, found := node.Status.Allocatable["memory"]; found {
			allocatableValues[node.Name] = nodeInfo{
				roles:       roles,
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
// for nodes fulfilling also the infra role it returns only an empty array
func getNodeRoles(node corev1.Node) (roles []string) {
	if _, isWorker := node.Labels[labelNodeRoleInfra]; isWorker {
		return
	}

	if _, isWorker := node.Labels[labelNodeRoleWorker]; isWorker {
		roles = append(roles, "worker")
	}

	if _, isMaster := node.Labels[labelNodeRoleMaster]; isMaster {
		roles = append(roles, "master")
	}
	return
}

// updateStatusConditions updates Member status conditions with the new conditions
func (r *Reconciler) updateStatusConditions(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, newConditions ...toolchainv1alpha1.Condition) error {
	// the controller should always update at least the last updated timestamp of the status so the status should be updated regardless of whether
	// any specific fields were updated. This way a problem with the controller can be indicated if the last updated timestamp was not updated.
	var conditionsWithTimestamps []toolchainv1alpha1.Condition
	for _, condition := range newConditions {
		condition.LastTransitionTime = metav1.Now()
		condition.LastUpdatedTime = &metav1.Time{Time: condition.LastTransitionTime.Time}
		conditionsWithTimestamps = append(conditionsWithTimestamps, condition)
	}
	memberStatus.Status.Conditions = conditionsWithTimestamps
	return r.Client.Status().Update(ctx, memberStatus)
}

func (r *Reconciler) setStatusReady(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus) error {
	return r.updateStatusConditions(
		ctx,
		memberStatus,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.ToolchainStatusAllComponentsReadyReason,
		})
}

func (r *Reconciler) setStatusNotReady(ctx context.Context, memberStatus *toolchainv1alpha1.MemberStatus, message string) error {
	return r.updateStatusConditions(
		ctx,
		memberStatus,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.ToolchainStatusComponentsNotReadyReason,
			Message: message,
		})
}

func (r *Reconciler) consoleURL(ctx context.Context, config membercfg.Configuration) (string, error) {
	route := &routev1.Route{}
	namespacedName := types.NamespacedName{Namespace: config.Console().Namespace(), Name: config.Console().RouteName()}
	if err := r.AllNamespacesClient.Get(ctx, namespacedName, route); err != nil {
		return "", err
	}
	return sanitizeURL(fmt.Sprintf("https://%s/%s", route.Spec.Host, route.Spec.Path)), nil
}

func sanitizeURL(url string) string {
	if strings.HasSuffix(url, "//") {
		return sanitizeURL(strings.TrimSuffix(url, "/")) // remove the extra `/`
	}
	return url
}
