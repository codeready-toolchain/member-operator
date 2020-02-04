package useraccount

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/go-logr/logr"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
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
	if err := c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, enqueueRequestForOwner); err != nil {
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
	var err error

	namespace := request.Namespace
	if namespace == "" {
		namespace, err = k8sutil.GetWatchNamespace()
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// Fetch the UserAccount instance
	userAcc := &toolchainv1alpha1.UserAccount{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: request.Name}, userAcc)
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
		reqLogger.Info("Adding finalizer on UserAccount")
		// Add the finalizer if it is not present
		if err := r.addFinalizer(userAcc); err != nil {
			return reconcile.Result{}, err
		}
	}
	if !util.IsBeingDeleted(userAcc) && !userAcc.Spec.Disabled {
		reqLogger.Info("Ensuring user and identity associated with UserAccount")
		var createdOrUpdated bool
		var user *userv1.User
		if user, createdOrUpdated, err = r.ensureUser(reqLogger, userAcc); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
		if _, createdOrUpdated, err = r.ensureIdentity(reqLogger, userAcc, user); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
		if _, createdOrUpdated, err = r.ensureNSTemplateSet(reqLogger, userAcc); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
	} else if util.HasFinalizer(userAcc, userAccFinalizerName) && util.IsBeingDeleted(userAcc) {
		reqLogger.Info("Terminating UserAccount")
		if err := r.setStatusTerminating(userAcc, "deleting user/identity"); err != nil {
			reqLogger.Error(err, "error updating status")
			return reconcile.Result{}, err
		}
		deleted, err := r.deleteIdentityAndUser(userAcc)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, userAcc, r.setStatusTerminating, err, "failed to delete user/identity")
		}

		// Remove finalizer from UserAccount
		if !deleted {
			util.RemoveFinalizer(userAcc, userAccFinalizerName)
			if err := r.client.Update(context.Background(), userAcc); err != nil {
				return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, userAcc, r.setStatusTerminating, err, "failed to remove finalizer")
			}

			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, nil
	} else if userAcc.Spec.Disabled {
		reqLogger.Info("Disabling UserAccount")
		if err := r.setStatusDisabling(userAcc, "deleting user/identity"); err != nil {
			reqLogger.Error(err, "error updating status")
			return reconcile.Result{}, err
		}
		deleted, err := r.deleteIdentityAndUser(userAcc)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(reqLogger, userAcc, r.setStatusDisabling, err, "failed to delete user/identity")
		}

		if !deleted {
			return reconcile.Result{}, r.setStatusDisabled(userAcc)
		}
		return reconcile.Result{}, nil
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

func (r *ReconcileUserAccount) ensureNSTemplateSet(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*toolchainv1alpha1.NSTemplateSet, bool, error) {
	name := userAcc.Name

	// create if not found
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name, Namespace: userAcc.Namespace}, nsTmplSet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new NSTemplateSet", "name", name)
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			nsTmplSet = newNSTemplateSet(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, nsTmplSet, r.scheme); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusNSTemplateSetCreationFailed, err,
					"failed to set controller reference for NSTemplateSet '%s'", name)
			}
			if err := r.client.Create(context.TODO(), nsTmplSet); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusNSTemplateSetCreationFailed, err,
					"failed to create NSTemplateSet '%s'", name)
			}
			logger.Info("NSTemplateSet created successfully", "name", name)
			return nsTmplSet, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusNSTemplateSetCreationFailed, err, "failed to get NSTemplateSet '%s'", name)
	}
	logger.Info("NSTemplateSet already exists", "name", name)

	// update if not same
	equal := nsTmplSet.Spec.CompareTo(userAcc.Spec.NSTemplateSet)
	if !equal {
		return nil, false, fmt.Errorf("update of NSTemplateSet is not supported")
	}

	// update status if ready=false
	readyCond, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	if !found || readyCond.Status == corev1.ConditionUnknown || (readyCond.Status == corev1.ConditionFalse && readyCond.Message == "") {
		return nsTmplSet, true, nil
	}
	if readyCond.Status == corev1.ConditionFalse {
		if err := r.setStatusFromNSTemplateSet(userAcc, readyCond.Reason, readyCond.Message); err != nil {
			return nsTmplSet, false, err
		}
		return nsTmplSet, true, nil
	}

	return nsTmplSet, false, nil
}

// setFinalizers sets the finalizers for UserAccount
func (r *ReconcileUserAccount) addFinalizer(userAcc *toolchainv1alpha1.UserAccount) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(userAcc, userAccFinalizerName) {
		util.AddFinalizer(userAcc, userAccFinalizerName)
		if err := r.client.Update(context.TODO(), userAcc); err != nil {
			return err
		}
	}

	return nil
}

// deleteIdentityAndUser deletes the identity and user. Returns bool and error indicating that
// whether the user/identity were deleted.
func (r *ReconcileUserAccount) deleteIdentityAndUser(userAcc *toolchainv1alpha1.UserAccount) (bool, error) {

	if deleted, err := r.deleteIdentity(userAcc); err != nil || deleted {
		return deleted, err
	}

	if deleted, err := r.deleteUser(userAcc); err != nil || deleted {
		return deleted, err
	}

	return false, nil
}

// deleteUser deletes the user resource. Returns `true` if the user was deleted, `false` otherwise,
// with the underlying error if the user existed and something wrong happened. If the user did not
// exist, this func returns `false, nil`
func (r *ReconcileUserAccount) deleteUser(userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	// Get the User associated with the UserAccount
	user := &userv1.User{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}

	// Delete User associated with UserAccount
	if err := r.client.Delete(context.TODO(), user); err != nil {
		return false, err
	}
	return true, nil
}

// deleteIdentity deletes the identity resource. Returns `true` if the identity was deleted, `false` otherwise,
// with the underlying error if the identity existed and something wrong happened. If the identity did not
// exist, this func returns `false, nil`
func (r *ReconcileUserAccount) deleteIdentity(userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	// Get the Identity associated with the UserAccount
	identity := &userv1.Identity{}
	identityName := ToIdentityName(userAcc.Spec.UserID)
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}

	// Delete Identity associated with UserAccount
	if err := r.client.Delete(context.TODO(), identity); err != nil {
		return false, err
	}

	return true, nil
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
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateUserReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusIdentityCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateIdentityReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusMappingCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateMappingReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusNSTemplateSetCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateNSTemplateSetReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusFromNSTemplateSet(userAcc *toolchainv1alpha1.UserAccount, reason, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusProvisioning(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountProvisioningReason,
		})
}

func (r *ReconcileUserAccount) setStatusReady(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.UserAccountProvisionedReason,
		})
}

func (r *ReconcileUserAccount) setStatusDisabling(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountDisablingReason,
			Message: message,
		})
}

func (r *ReconcileUserAccount) setStatusDisabled(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountDisabledReason,
		})
}

func (r *ReconcileUserAccount) setStatusTerminating(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountTerminatingReason,
			Message: message,
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

func newNSTemplateSet(userAcc *toolchainv1alpha1.UserAccount) *toolchainv1alpha1.NSTemplateSet {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userAcc.Name,
			Namespace: userAcc.Namespace,
		},
		Spec: userAcc.Spec.NSTemplateSet,
	}
	return nsTmplSet
}

// ToIdentityName converts the given `userID` into an identity
func ToIdentityName(userID string) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userID)
}
