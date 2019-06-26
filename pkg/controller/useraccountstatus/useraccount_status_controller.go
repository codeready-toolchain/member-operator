package useraccountstatus

import (
	"context"
	"fmt"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/kubefed/pkg/controller/util"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileUserAccountStatus{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		getHostCluster: cluster.GetFirstFedCluster,
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
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.UserAccount{}}, &handler.EnqueueRequestForObject{}, GenerationNotChangedPredicate{})
	if err != nil {
		return err
	}

	return nil
}

type GenerationNotChangedPredicate struct {
	predicate.GenerationChangedPredicate
}

func (p GenerationNotChangedPredicate) Update(e event.UpdateEvent) bool {
	return p.GenerationChangedPredicate.Update(e)
}

// blank assignment to verify that ReconcileUserAccountStatus implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileUserAccountStatus{}

// ReconcileUserAccountStatus reconciles a UserAccount object
type ReconcileUserAccountStatus struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client         client.Client
	scheme         *runtime.Scheme
	getHostCluster func() (*cluster.FedCluster, bool)
}

// Reconcile watches changes in status of UserAccount object
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileUserAccountStatus) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling UserAccountStatus")

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

	err = r.updateMasterUserRecord(userAcc)
	if err != nil {
		reqLogger.Error(err, "unable to update the master user record")
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileUserAccountStatus) updateMasterUserRecord(userAcc *toolchainv1alpha1.UserAccount) error {
	fedCluster, ok := r.getHostCluster()
	if !ok {
		return fmt.Errorf("there is no host cluster registered")
	}
	if !util.IsClusterReady(fedCluster.ClusterStatus) {
		return fmt.Errorf("the host cluster is not ready")
	}
	userRecord := &toolchainv1alpha1.MasterUserRecord{}
	name := types.NamespacedName{Namespace: fedCluster.OperatorNamespace, Name: userAcc.Name}
	err := fedCluster.Client.Get(context.TODO(), name, userRecord)
	if err != nil {
		return err
	}
	for i, account := range userRecord.Spec.UserAccounts {
		if account.TargetCluster == fedCluster.LocalName {
			userRecord.Spec.UserAccounts[i].SyncIndex = userAcc.ResourceVersion
			return fedCluster.Client.Update(context.TODO(), userRecord)
		}
	}
	return fmt.Errorf("the MasterUserRecord doesn't have UserAccount embedded for the cluster %s", fedCluster.LocalName)
}
