package useraccount

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
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

const (
	// Status condition reasons
	unableToCreateUserReason     = "UnableToCreateUser"
	unableToCreateIdentityReason = "UnableToCreateIdentity"
	unableToCreateMappingReason  = "UnableToCreateMapping"
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

	var createdOrUpdated bool
	var user *userv1.User
	if user, createdOrUpdated, err = r.ensureUser(reqLogger, userAcc); err != nil || createdOrUpdated {
		return reconcile.Result{}, err
	}

	if _, createdOrUpdated, err = r.ensureIdentity(reqLogger, userAcc, user); err != nil || createdOrUpdated {
		return reconcile.Result{}, err
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
			if err := r.setStatusUserCreationOK(userAcc); err != nil {
				return nil, false, err
			}
			logger.Info("user created successfully", "name", userAcc.Name)
			return user, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusUserCreationFailed, err, "failed to get user '%s'", userAcc.Name)
	}
	logger.Info("user already exists", "name", userAcc.Name)

	// ensure mapping
	if user.Identities == nil || len(user.Identities) < 1 || user.Identities[0] != getIdentityName(userAcc) {
		logger.Info("user is missing a reference to identity; updating the reference", "name", userAcc.Name)
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		user.Identities = []string{getIdentityName(userAcc)}
		if err := r.client.Update(context.TODO(), user); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusMappingCreationFailed, err, "failed to update user '%s'", userAcc.Name)
		}
		if err := r.setStatusMappingCreationOK(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("user updated successfully", "name", userAcc.Name)
		return user, true, nil
	}
	return user, false, nil
}

func (r *ReconcileUserAccount) ensureIdentity(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) (*userv1.Identity, bool, error) {
	name := getIdentityName(userAcc)
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
			if err := r.setStatusIdentityCreationOK(userAcc); err != nil {
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
		if err := r.setStatusMappingCreationOK(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("identity updated successfully", "name", name)
		return identity, true, nil
	}

	return identity, false, nil
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
	reason := unableToCreateUserReason
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.UserAccountUserNotReady,
			Status:  corev1.ConditionTrue,
			Reason:  reason,
			Message: message,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountReady,
			Status: corev1.ConditionFalse,
			Reason: reason,
		})
}

func (r *ReconcileUserAccount) setStatusIdentityCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	reason := unableToCreateIdentityReason
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.UserAccountIdentityNotReady,
			Status:  corev1.ConditionTrue,
			Reason:  reason,
			Message: message,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountReady,
			Status: corev1.ConditionFalse,
			Reason: reason,
		})
}

func (r *ReconcileUserAccount) setStatusMappingCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	reason := unableToCreateMappingReason
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.UserAccountUserIdentityMappingNotReady,
			Status:  corev1.ConditionTrue,
			Reason:  reason,
			Message: message,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountReady,
			Status: corev1.ConditionFalse,
			Reason: reason,
		})
}

func (r *ReconcileUserAccount) setStatusUserCreationOK(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountUserNotReady,
			Status: corev1.ConditionFalse,
		})
}

func (r *ReconcileUserAccount) setStatusIdentityCreationOK(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountIdentityNotReady,
			Status: corev1.ConditionFalse,
		})
}

func (r *ReconcileUserAccount) setStatusMappingCreationOK(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountUserIdentityMappingNotReady,
			Status: corev1.ConditionFalse,
		})
}

func (r *ReconcileUserAccount) setStatusProvisioning(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountProvisioning,
			Status: corev1.ConditionTrue,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountReady,
			Status: corev1.ConditionFalse,
		})
}

func (r *ReconcileUserAccount) setStatusReady(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountProvisioning,
			Status: corev1.ConditionFalse,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountUserNotReady,
			Status: corev1.ConditionFalse,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountIdentityNotReady,
			Status: corev1.ConditionFalse,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountUserIdentityMappingNotReady,
			Status: corev1.ConditionFalse,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountNSTemplateSetNotReady,
			Status: corev1.ConditionFalse,
		},
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.UserAccountReady,
			Status: corev1.ConditionTrue,
		})
}

// updateStatusConditions updates user account status conditions with the new conditions
func (r *ReconcileUserAccount) updateStatusConditions(userAcc *toolchainv1alpha1.UserAccount, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	userAcc.Status.Conditions, updated = AddOrUpdateStatusConditions(userAcc.Status.Conditions, newConditions...)
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
		Identities: []string{getIdentityName(userAcc)},
	}
	return user
}

func newIdentity(userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) *userv1.Identity {
	name := getIdentityName(userAcc)
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

func getIdentityName(userAcc *toolchainv1alpha1.UserAccount) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID)
}

// AddOrUpdateStatusConditions appends the new conditions to the condition slice. If there is already a condition
// with the same type in the current condition array then the condition is updated in the result slice.
// If the condition is not changed then the same unmodified slice is returned.
// Also returns a bool flag which indicates if the conditions where updated/added
// TODO: move to toolchain-common
func AddOrUpdateStatusConditions(conditions []toolchainv1alpha1.Condition, newConditions ...toolchainv1alpha1.Condition) ([]toolchainv1alpha1.Condition, bool) {
	var atLeastOneUpdated bool
	var updated bool
	for _, cond := range newConditions {
		conditions, updated = addOrUpdateStatusCondition(conditions, cond)
		atLeastOneUpdated = atLeastOneUpdated || updated
	}

	return conditions, atLeastOneUpdated
}

// TODO: move to toolchain-common
func addOrUpdateStatusCondition(conditions []toolchainv1alpha1.Condition, newCondition toolchainv1alpha1.Condition) ([]toolchainv1alpha1.Condition, bool) {
	newCondition.LastTransitionTime = metav1.Now()

	if conditions == nil {
		return []toolchainv1alpha1.Condition{newCondition}, true
	} else {
		for i, cond := range conditions {
			if cond.Type == newCondition.Type {
				// Condition already present. Update it if needed.
				if cond.Status == newCondition.Status &&
					cond.Reason == newCondition.Reason &&
					cond.Message == newCondition.Message {
					// Nothing changed. No need to update.
					return conditions, false
				}

				// Update LastTransitionTime only if the status changed otherwise keep the old time
				if newCondition.Status == cond.Status {
					newCondition.LastTransitionTime = cond.LastTransitionTime
				}
				// Don't modify the currentConditions slice. Generate a new slice instead.
				res := make([]toolchainv1alpha1.Condition, len(conditions))
				copy(res, conditions)
				res[i] = newCondition
				return res, true
			}
		}
	}
	return append(conditions, newCondition), true
}
