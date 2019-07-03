package useraccount

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"

	"github.com/codeready-toolchain/member-operator/pkg/common"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/go-logr/logr"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_useraccount")

// Add creates a new UserAccount Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileUserAccount{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	c, err := controller.New("useraccount-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource UserAccount
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.UserAccount{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource
	enqueueRequestForOwner := &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &toolchainv1alpha1.UserAccount{},
	}
	if err := c.Watch(&source.Kind{Type: &userv1.User{}}, enqueueRequestForOwner); err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &userv1.Identity{}}, enqueueRequestForOwner); err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileUserAccount{}

// ReconcileUserAccount reconciles a UserAccount object
type ReconcileUserAccount struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a UserAccount object and makes changes based on the state read
// and what is in the UserAccount.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileUserAccount) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling UserAccount")

	// Fetch the UserAccount instance
	userAcc := &toolchainv1alpha1.UserAccount{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: config.GetOperatorNamespace(), Name: request.Name}, userAcc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	var created bool
	var user *userv1.User
	if user, created, err = r.ensureUser(reqLogger, userAcc); err != nil || created {
		return reconcile.Result{}, err
	}

	var identity *userv1.Identity
	if identity, created, err = r.ensureIdentity(reqLogger, userAcc); err != nil || created {
		return reconcile.Result{}, err
	}

	if created, err = r.ensureMapping(reqLogger, userAcc, user, identity); err != nil || created {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, r.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioned, "")
}

func (r *ReconcileUserAccount) ensureUser(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, bool, error) {
	user := &userv1.User{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new user", "name", userAcc.Name)
			if err := r.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, ""); err != nil {
				return nil, false, err
			}
			user = newUser(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, user, r.scheme); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to set controller reference for user '%s'", userAcc.Name)
			}
			if err := r.client.Create(context.TODO(), user); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to create user '%s'", userAcc.Name)
			}
			logger.Info("user created successfully", "name", userAcc.Name)
			return user, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to get user '%s'", userAcc.Name)
	}
	logger.Info("user already exists", "name", userAcc.Name)
	return user, false, nil
}

func (r *ReconcileUserAccount) ensureIdentity(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*userv1.Identity, bool, error) {
	name := common.ToIdentityName(userAcc.Spec.UserID)
	identity := &userv1.Identity{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: name}, identity); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new identity", "name", name)
			if err := r.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, ""); err != nil {
				return nil, false, err
			}
			identity = newIdentity(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, identity, r.scheme); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to set controller reference for identity '%s'", name)
			}
			if err := r.client.Create(context.TODO(), identity); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to create identity '%s'", name)
			}
			logger.Info("identity created successfully", "name", name)
			return identity, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to get identity '%s'", name)
	}
	logger.Info("identity already exists", "name", name)
	return identity, false, nil
}

func (r *ReconcileUserAccount) ensureMapping(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, user *userv1.User, identity *userv1.Identity) (bool, error) {
	mapping := newMapping(user, identity)
	name := mapping.Name
	if err := controllerutil.SetControllerReference(userAcc, mapping, r.scheme); err != nil {
		return false, err
	}

	if err := r.client.Create(context.TODO(), mapping); err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Info("user-identity mapping already exists", "name", name)
			return false, nil
		}
		return false, r.wrapErrorWithStatusUpdate(logger, userAcc, err, "failed to create user-identity mapping '%s'", name)
	}
	logger.Info("user-identity mapping created successfully", "name", name)
	return true, r.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, "")
}

// updateStatus updates user account status to given status with errMsg but only if the current status doesn't match
// If the current status already set to desired state then this method does nothing
func (r *ReconcileUserAccount) updateStatus(userAcc *toolchainv1alpha1.UserAccount, status toolchainv1alpha1.StatusUserAccount, errMsg string) error {
	if userAcc.Status.Status == status && userAcc.Status.Error == errMsg {
		// Nothing changed
		return nil
	}
	userAcc.Status = toolchainv1alpha1.UserAccountStatus{
		Status: status,
		Error:  errMsg,
	}
	return r.client.Status().Update(context.TODO(), userAcc)
}

// wrapErrorWithStatusUpdate wraps the error and set the user account status to "provisioning". If the update failed then log the error.
func (r *ReconcileUserAccount) wrapErrorWithStatusUpdate(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := r.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func newUser(userAcc *toolchainv1alpha1.UserAccount) *userv1.User {
	user := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: userAcc.Name,
		},
	}
	return user
}

func newIdentity(userAcc *toolchainv1alpha1.UserAccount) *userv1.Identity {
	name := common.ToIdentityName(userAcc.Spec.UserID)
	identity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		ProviderName:     config.GetIdP(),
		ProviderUserName: userAcc.Spec.UserID,
	}
	return identity
}

func newMapping(user *userv1.User, identity *userv1.Identity) *userv1.UserIdentityMapping {
	name := identity.Name
	mapping := &userv1.UserIdentityMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		User: corev1.ObjectReference{
			Name: user.Name,
			UID:  user.UID,
		},
		Identity: corev1.ObjectReference{
			Name: identity.Name,
			UID:  identity.UID,
		},
	}
	return mapping
}
