package useraccountstatus

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/predicate"
	"github.com/go-logr/logr"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

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

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return add(mgr, r)
}

// Reconciler reconciles a UserAccount object
type Reconciler struct {
	Client         client.Client
	Log            logr.Logger
	Scheme         *runtime.Scheme
	GetHostCluster func() (*cluster.CachedToolchainCluster, bool)
}

// Reconcile watches changes in status of UserAccount object
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *Reconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := r.Log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("reconciling UserAccountStatus")

	// Fetch the UserAccount object
	userAcc := &toolchainv1alpha1.UserAccount{}
	err := r.Client.Get(context.TODO(), request.NamespacedName, userAcc)
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

func (r *Reconciler) updateMasterUserRecord(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*toolchainv1alpha1.MasterUserRecord, error) {
	if userAcc.DeletionTimestamp != nil {
		logger.Info("Updating MUR after UserAccount deletion")
	} else {
		logger.Info("Updating MUR")
	}
	cachedToolchainCluster, ok := r.GetHostCluster()
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
