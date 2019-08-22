package useraccount

import (
	"context"
	"fmt"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/go-logr/logr"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"

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

const (
	// Status condition reasons
	unableToCreateUserReason     = "UnableToCreateUser"
	unableToCreateIdentityReason = "UnableToCreateIdentity"
	unableToCreateMappingReason  = "UnableToCreateMapping"
	provisioningReason           = "Provisioning"
	provisionedReason            = "Provisioned"

	// Finalizers
	userAccFinalizerName = "finalizer.toolchain.dev.openshift.com"
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

	// If the UserAccount has not been deleted, create or update user and identity resources.
	// If the UserAccount has been deleted, delete secondary resources identity and user.
	if !util.IsBeingDeleted(userAcc) {
		// Add the finalizer if it is not present
		if err := r.addFinalizer(userAcc, userAccFinalizerName); err != nil {
			return reconcile.Result{}, err
		}

		var createdOrUpdated bool
		var user *userv1.User
		if user, createdOrUpdated, err = r.ensureUser(reqLogger, userAcc); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}

		if _, createdOrUpdated, err = r.ensureIdentity(reqLogger, userAcc, user); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
	} else if util.HasFinalizer(userAcc, userAccFinalizerName) {
		if err = r.manageCleanUp(reqLogger, userAcc); err != nil {
			return reconcile.Result{}, err
		}
	}
	return reconcile.Result{}, r.setStatusReady(userAcc)
}

func (r *ReconcileUserAccount) ensureUser(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, bool, error) {
	user := &userv1.User{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new user", "name", userAcc.Name)
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			user = newUser(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, user, r.scheme); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusUserCreationFailed, err, "failed to set controller reference for user '%s'", userAcc.Name)
			}
			if err := r.client.Create(context.TODO(), user); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusUserCreationFailed, err, "failed to create user '%s'", userAcc.Name)
			}
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			logger.Info("user created successfully", "name", userAcc.Name)
			return user, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusUserCreationFailed, err, "failed to get user '%s'", userAcc.Name)
	}
	logger.Info("user already exists", "name", userAcc.Name)

	// ensure mapping
	if user.Identities == nil || len(user.Identities) < 1 || user.Identities[0] != ToIdentityName(userAcc.Spec.UserID) {
		logger.Info("user is missing a reference to identity; updating the reference", "name", userAcc.Name)
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		user.Identities = []string{ToIdentityName(userAcc.Spec.UserID)}
		if err := r.client.Update(context.TODO(), user); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusMappingCreationFailed, err, "failed to update user '%s'", userAcc.Name)
		}
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("user updated successfully", "name", userAcc.Name)
		return user, true, nil
	}
	return user, false, nil
}

func (r *ReconcileUserAccount) ensureIdentity(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) (*userv1.Identity, bool, error) {
	name := ToIdentityName(userAcc.Spec.UserID)
	identity := &userv1.Identity{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: name}, identity); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new identity", "name", name)
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			identity = newIdentity(userAcc, user)
			if err := controllerutil.SetControllerReference(userAcc, identity, r.scheme); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusIdentityCreationFailed, err, "failed to set controller reference for identity '%s'", name)
			}
			if err := r.client.Create(context.TODO(), identity); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusIdentityCreationFailed, err, "failed to create identity '%s'", name)
			}
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			logger.Info("identity created successfully", "name", name)
			return identity, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusIdentityCreationFailed, err, "failed to get identity '%s'", name)
	}
	logger.Info("identity already exists", "name", name)

	// ensure mapping
	if identity.User.Name != user.Name || identity.User.UID != user.UID {
		logger.Info("identity is missing a reference to user; updating the reference", "identity", name, "user", user.Name)
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		identity.User = corev1.ObjectReference{
			Name: user.Name,
			UID:  user.UID,
		}
		if err := r.client.Update(context.TODO(), identity); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusMappingCreationFailed, err, "failed to update identity '%s'", name)
		}
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("identity updated successfully", "name", name)
		return identity, true, nil
	}

	return identity, false, nil
}

// setFinalizers sets the finalizers for UserAccount
func (r *ReconcileUserAccount) addFinalizer(userAcc *toolchainv1alpha1.UserAccount, finalizer string) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(userAcc, userAccFinalizerName) {
		util.AddFinalizer(userAcc, userAccFinalizerName)
		if err := r.client.Update(context.TODO(), userAcc); err != nil {
			return err
		}
	}

	return nil
}

// manageCleanUp deletes the identity, user and finalizer when the UserAccount is being deleted
func (r *ReconcileUserAccount) manageCleanUp(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) error {
	var deleted bool
	var err error
	if err, deleted = r.deleteIdentity(logger, userAcc); err != nil || deleted {
		return err
	}

	if err, deleted = r.deleteUser(logger, userAcc); err != nil || deleted {
		return err
	}

	// Remove finalizer from UserAccount
	util.RemoveFinalizer(userAcc, userAccFinalizerName)
	if err := r.client.Update(context.Background(), userAcc); err != nil {
		return err
	}

	return nil
}

// deleteUser deletes the user resource
func (r *ReconcileUserAccount) deleteUser(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (error, bool) {
	// Get the User associated with the UserAccount
	user := &userv1.User{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err, false
		} else {
			return nil, false
		}
	}

	// Delete User associated with UserAccount
	if err := r.client.Delete(context.TODO(), user); err != nil {
		return err, false
	}
	return nil, true
}

// deleteIdentity deletes the identity resource.
func (r *ReconcileUserAccount) deleteIdentity(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (error, bool) {
	// Get the Identity associated with the UserAccount
	identity := &userv1.Identity{}
	identityName := ToIdentityName(userAcc.Spec.UserID)
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err, false
		} else {
			return nil, false
		}
	}

	// Delete Identity associated with UserAccount
	if err := r.client.Delete(context.TODO(), identity); err != nil {
		return err, false
	}

	return nil, true
}

// wrapErrorWithStatusUpdate wraps the error and update the user account status. If the update failed then logs the error.
func (r *ReconcileUserAccount) wrapErrorWithStatusUpdate(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, statusUpdater func(userAcc *toolchainv1alpha1.UserAccount, message string) error, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(userAcc, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *ReconcileUserAccount) setStatusUserCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  unableToCreateUserReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusIdentityCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  unableToCreateIdentityReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusMappingCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  unableToCreateMappingReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusProvisioning(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: provisioningReason,
		})
}

func (r *ReconcileUserAccount) setStatusReady(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: provisionedReason,
		})
}

// updateStatusConditions updates user account status conditions with the new conditions
func (r *ReconcileUserAccount) updateStatusConditions(userAcc *toolchainv1alpha1.UserAccount, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	userAcc.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(userAcc.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.client.Status().Update(context.TODO(), userAcc)
}

func newUser(userAcc *toolchainv1alpha1.UserAccount) *userv1.User {
	user := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: userAcc.Name,
		},
		Identities: []string{ToIdentityName(userAcc.Spec.UserID)},
	}
	return user
}

func newIdentity(userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) *userv1.Identity {
	name := ToIdentityName(userAcc.Spec.UserID)
	identity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		ProviderName:     config.GetIdP(),
		ProviderUserName: userAcc.Spec.UserID,
		User: corev1.ObjectReference{
			Name: user.Name,
			UID:  user.UID,
		},
	}
	return identity
}

func ToIdentityName(userID string) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userID)
}
