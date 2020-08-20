package useraccountstatus

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/pkg/predicate"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/go-logr/logr"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_useraccount_status")

// Add creates a new UserAccountStatus Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, _ *configuration.Config) error {
	metricsClient, err := versioned.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	return add(mgr, newReconciler(mgr, metricsClient))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, metricsClient *versioned.Clientset) reconcile.Reconciler {
	return &ReconcileUserAccountStatus{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		getHostCluster: cluster.GetHostCluster,
		metricsClient:  metricsClient,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("useraccount_status-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource UserAccountStatus
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.UserAccount{}}, &handler.EnqueueRequestForObject{}, predicate.OnlyUpdateWhenGenerationNotChanged{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileUserAccountStatus implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileUserAccountStatus{}

// ReconcileUserAccountStatus reconciles a UserAccount object
type ReconcileUserAccountStatus struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client         client.Client
	metricsClient  *versioned.Clientset
	scheme         *runtime.Scheme
	getHostCluster func() (*cluster.CachedToolchainCluster, bool)
}

// Reconcile watches changes in status of UserAccount object
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileUserAccountStatus) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("reconciling UserAccountStatus")

	// Fetch the UserAccount object
	userAcc := &toolchainv1alpha1.UserAccount{}
	err := r.client.Get(context.TODO(), request.NamespacedName, userAcc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	mur, err := r.updateMasterUserRecord(logger, userAcc)
	if err != nil {
		if mur != nil {
			logger.Error(err, "unable to update the master user record", "MasterUserRecord", mur, "UserAccount", userAcc)
		} else {
			logger.Error(err, "unable to get the master user record for the UserAccount", "UserAccount", userAcc)
		}
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileUserAccountStatus) updateMasterUserRecord(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*toolchainv1alpha1.MasterUserRecord, error) {
	if userAcc.DeletionTimestamp != nil {
		logger.Info("Updating MUR after UserAccount deletion")
	} else {
		logger.Info("Updating MUR")
	}
	cachedToolchainCluster, ok := r.getHostCluster()
	if !ok {
		return nil, fmt.Errorf("there is no host cluster registered")
	}
	if !cluster.IsReady(cachedToolchainCluster.ClusterStatus) {
		return nil, fmt.Errorf("the host cluster is not ready")
	}
	mur := &toolchainv1alpha1.MasterUserRecord{}
	name := types.NamespacedName{Namespace: cachedToolchainCluster.OperatorNamespace, Name: userAcc.Name}
	if err := cachedToolchainCluster.Client.Get(context.TODO(), name, mur); err != nil {
		return nil, err
	}
	for i, account := range mur.Spec.UserAccounts {
		if account.TargetCluster == cachedToolchainCluster.OwnerClusterName {
			if util.IsBeingDeleted(userAcc) {
				mur.Spec.UserAccounts[i].SyncIndex = "0"
			} else {
				mur.Spec.UserAccounts[i].SyncIndex = userAcc.ResourceVersion
			}
			return mur, cachedToolchainCluster.Client.Update(context.TODO(), mur)
		}
	}
	return mur, fmt.Errorf("the MasterUserRecord '%s' doesn't have any embedded UserAccount for cluster '%s'", mur.Name, cachedToolchainCluster.OwnerClusterName)
}
