package useraccount

import (
	"context"
	"fmt"
	"reflect"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	membercfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/controllers"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"

	"github.com/go-logr/logr"
	userv1 "github.com/openshift/api/user/v1"
	errs "github.com/pkg/errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	mapToOwnerByLabel := handler.EnqueueRequestsFromMapFunc(commoncontroller.MapToOwnerByLabel("", toolchainv1alpha1.OwnerLabelKey))

	return ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.UserAccount{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{}).
		Watches(&source.Kind{Type: &userv1.User{}}, mapToOwnerByLabel).
		Watches(&source.Kind{Type: &userv1.Identity{}}, mapToOwnerByLabel).
		Complete(r)
}

// Reconciler reconciles a UserAccount object
type Reconciler struct {
	Client    client.Client
	Scheme    *runtime.Scheme
	CheClient *che.Client
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=useraccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=useraccounts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=useraccounts/finalizers,verbs=update

//+kubebuilder:rbac:groups=user.openshift.io,resources=identities;users;useridentitymappings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;update;patch;create;delete

// Reconcile reads that state of the cluster for a UserAccount object and makes changes based on the state read
// and what is in the UserAccount.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling UserAccount")
	var err error

	namespace := request.Namespace
	if namespace == "" {
		namespace, err = commonconfig.GetWatchNamespace()
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// retrieve the latest config and use it for this reconciliation
	config, err := membercfg.GetConfiguration(r.Client)
	if err != nil {
		return reconcile.Result{}, errs.Wrapf(err, "unable to get MemberOperatorConfig")
	}

	// Fetch the UserAccount instance
	userAcc := &toolchainv1alpha1.UserAccount{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: request.Name}, userAcc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// If the UserAccount has not been deleted, create or update user and Identity resources.
	// If the UserAccount has been deleted, delete secondary resources identity and user.
	if !util.IsBeingDeleted(userAcc) {
		// Add the finalizer if it is not present
		if err := r.addFinalizer(logger, userAcc); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		return reconcile.Result{}, r.ensureUserAccountDeletion(logger, config, userAcc)
	}
	if !userAcc.Spec.Disabled {
		logger.Info("ensuring user and identity associated with UserAccount")
		var createdOrUpdated bool
		var user *userv1.User
		if user, createdOrUpdated, err = r.ensureUser(logger, config, userAcc); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
		if _, createdOrUpdated, err = r.ensureIdentity(logger, config, userAcc, user); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
		if _, createdOrUpdated, err = r.ensureNSTemplateSet(logger, userAcc); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
	} else {
		logger.Info("Disabling UserAccount")
		if err := r.setStatusDisabling(userAcc, "deleting user/identity"); err != nil {
			logger.Error(err, "error updating status")
			return reconcile.Result{}, err
		}
		deleted, err := r.deleteIdentityAndUser(logger, config, userAcc)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusDisabling, err, "failed to delete user/identity")
		}

		if !deleted {
			return reconcile.Result{}, r.setStatusDisabled(userAcc)
		}
		return reconcile.Result{}, nil
	}

	// check what the current ready condition is set to
	readyCond, ok := condition.FindConditionByType(userAcc.Status.Conditions, toolchainv1alpha1.ConditionReady)
	// if the condition is present, has updating reason and was set less than 1 second ago
	if ok && readyCond.Reason == toolchainv1alpha1.UserAccountUpdatingReason && time.Since(readyCond.LastTransitionTime.Time) <= time.Second {
		// then don't do anything and just postpone the next reconcile
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: time.Second,
		}, nil
	}
	logger.Info("All provisioned - setting status to ready.")
	return reconcile.Result{}, r.setStatusReady(userAcc)
}

func (r *Reconciler) ensureUserAccountDeletion(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) error {
	if util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName) {
		logger.Info("terminating UserAccount")
		// We need to be sure that the status is updated when the UserAccount is deleted.
		// In this case the UserAccountStatus controller updates the MUR on the host cluster
		// In turn, the MUR controller may decide to recreate the UserAccount resource on the
		// member cluster.

		// Clean up Che resources by deleting the Che user (required for GDPR and reactivation of users)
		if err := r.lookupAndDeleteCheUser(logger, config, userAcc); err != nil {
			return r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusTerminating, err, "failed to delete Che user data")
		}

		deleted, err := r.deleteIdentityAndUser(logger, config, userAcc)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusTerminating, err, "failed to delete user/identity")
		}
		if deleted {
			if err := r.setStatusTerminating(userAcc, "deleting user/identity"); err != nil {
				logger.Error(err, "error updating status")
				return err
			}
			return nil
		}

		deleted, err = r.deleteNSTemplateSet(logger, userAcc)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusTerminating, err, "failed to delete the NSTemplateSet")
		}
		if deleted {
			if err := r.setStatusTerminating(userAcc, "deleting NSTemplateSet"); err != nil {
				logger.Error(err, "error updating status")
				return err
			}
			return nil
		}

		// Remove finalizer from UserAccount
		util.RemoveFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(context.Background(), userAcc); err != nil {
			return r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusTerminating, err, "failed to remove finalizer")
		}
		// no need to update the status of the UserAccount once the finalizer has been removed, since
		// the resource will be deleted
		logger.Info("removed finalizer")
	}
	return nil
}

func (r *Reconciler) ensureUser(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, bool, error) {
	user := &userv1.User{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new user", "name", userAcc.Name)
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			user = newUser(userAcc, config)
			setOwnerLabel(user, userAcc.Name)
			if err := r.Client.Create(context.TODO(), user); err != nil {
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
	logger.Info("user already exists")

	// ensure mapping
	if user.Identities == nil || len(user.Identities) < 1 || user.Identities[0] != ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()) {
		logger.Info("user is missing a reference to identity; updating the reference", "name", userAcc.Name)
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		user.Identities = []string{ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}
		if err := r.Client.Update(context.TODO(), user); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusMappingCreationFailed, err, "failed to update user '%s'", userAcc.Name)
		}
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("user updated successfully")
		return user, true, nil
	}
	return user, false, nil
}

func (r *Reconciler) ensureIdentity(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) (*userv1.Identity, bool, error) {
	name := ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())
	identity := &userv1.Identity{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: name}, identity); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new identity")
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			identity = newIdentity(userAcc, user, config)
			setOwnerLabel(identity, userAcc.Name)
			if err := r.Client.Create(context.TODO(), identity); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusIdentityCreationFailed, err, "failed to create identity '%s'", name)
			}
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			logger.Info("identity created successfully")
			return identity, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusIdentityCreationFailed, err, "failed to get identity '%s'", name)
	}
	logger.Info("identity already exists")

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
		if err := r.Client.Update(context.TODO(), identity); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusMappingCreationFailed, err, "failed to update identity '%s'", name)
		}
		if err := r.setStatusProvisioning(userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("identity updated successfully")
		return identity, true, nil
	}

	return identity, false, nil
}

func setOwnerLabel(object metav1.Object, owner string) {
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[toolchainv1alpha1.OwnerLabelKey] = owner
	object.SetLabels(labels)
}

func (r *Reconciler) ensureNSTemplateSet(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (*toolchainv1alpha1.NSTemplateSet, bool, error) {
	if userAcc.Spec.NSTemplateSet == nil {
		return nil, false, nil
	}
	name := userAcc.Name

	// create if not found
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name, Namespace: userAcc.Namespace}, nsTmplSet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new NSTemplateSet", "name", name)
			if err := r.setStatusProvisioning(userAcc); err != nil {
				return nil, false, err
			}
			nsTmplSet = newNSTemplateSet(userAcc)
			if err := r.Client.Create(context.TODO(), nsTmplSet); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusNSTemplateSetCreationFailed, err,
					"failed to create NSTemplateSet '%s'", name)
			}
			logger.Info("NSTemplateSet created successfully")
			return nsTmplSet, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusNSTemplateSetCreationFailed, err, "failed to get NSTemplateSet '%s'", name)
	}
	logger.Info("NSTemplateSet already exists")

	// update if not same
	if !reflect.DeepEqual(nsTmplSet.Spec, *userAcc.Spec.NSTemplateSet) {
		return r.updateNSTemplateSet(logger, userAcc, nsTmplSet)
	}
	logger.Info("NSTemplateSet is up-to-date", "name", name)

	readyCond, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	// if UserAccount ready condition is already set to false, then don't update if no message is provided
	// also, don't update when NSTemplateSet ready condition is in an invalid state
	if !found || readyCond.Status == corev1.ConditionUnknown ||
		(condition.IsFalse(userAcc.Status.Conditions, toolchainv1alpha1.ConditionReady) && readyCond.Status == corev1.ConditionFalse && readyCond.Message == "") {
		logger.Info("Either NSTemplateSet is an invalid state or UserAccount ready condition is already set to false and no message is provided by NSTemplateSet", "nstemplateset-ready-condition", readyCond)
		return nsTmplSet, true, nil
	}

	if readyCond.Status == corev1.ConditionFalse {
		logger.Info("NSTemplateSet is not ready", "ready-condition", readyCond)
		if err := r.setStatusFromNSTemplateSet(userAcc, readyCond.Reason, readyCond.Message); err != nil {
			return nsTmplSet, false, err
		}
		return nsTmplSet, true, nil
	}

	return nsTmplSet, false, nil
}

func (r *Reconciler) updateNSTemplateSet(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, nsTmplSet *toolchainv1alpha1.NSTemplateSet) (*toolchainv1alpha1.NSTemplateSet, bool, error) {
	if err := r.setStatusUpdating(userAcc); err != nil {
		return nil, false, err
	}

	nsTmplSet.Spec = *userAcc.Spec.NSTemplateSet

	if err := r.Client.Update(context.TODO(), nsTmplSet); err != nil {
		return nil, false, r.wrapErrorWithStatusUpdate(logger, userAcc, r.setStatusUpdateFailed, err,
			"failed to update NSTemplateSet '%s'", nsTmplSet.Name)
	}
	logger.Info("NSTemplateSet updated successfully", "name", nsTmplSet.Name)
	return nsTmplSet, true, nil
}

// setFinalizers sets the finalizers for UserAccount
func (r *Reconciler) addFinalizer(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName) {
		logger.Info("adding finalizer on UserAccount")
		util.AddFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(context.TODO(), userAcc); err != nil {
			return err
		}
	}

	return nil
}

// deleteIdentityAndUser deletes the identity and user.
// Returns bool and error indicating that whether the user/identity were deleted.
func (r *Reconciler) deleteIdentityAndUser(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	if deleted, err := r.deleteIdentity(logger, config, userAcc); err != nil || deleted {
		return deleted, err
	}
	if deleted, err := r.deleteUser(logger, userAcc); err != nil || deleted {
		return deleted, err
	}
	return false, nil
}

// deleteUser deletes the user resource.
// Returns `true` if the user was deleted, `false` otherwise, with the underlying error
// if the user existed and something wrong happened. If the user did not exist,
// this func returns `false, nil`
func (r *Reconciler) deleteUser(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	// Get the User associated with the UserAccount
	user := &userv1.User{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	logger.Info("deleting the User resource")
	// Delete User associated with UserAccount
	if err := r.Client.Delete(context.TODO(), user); err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	logger.Info("deleted the User resource")
	return true, nil
}

// deleteIdentity deletes the Identity resource.
// Returns `true` if the identity was deleted, `false` otherwise, with the underlying error
// if the identity existed and something wrong happened. If the identity did not exist,
// this func returns `false, nil`
func (r *Reconciler) deleteIdentity(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	// Get the Identity associated with the UserAccount
	identity := &userv1.Identity{}
	identityName := ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	logger.Info("deleting the Identity resource")
	// Delete Identity associated with UserAccount
	if err := r.Client.Delete(context.TODO(), identity); err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	logger.Info("deleted the Identity resource")
	return true, nil
}

// deleteNSTemplateSet deletes the NSTemplateSet associated with the given UserAccount (if specified)
// Returns bool and error indicating that whether the resource were deleted.
func (r *Reconciler) deleteNSTemplateSet(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	if userAcc.Spec.NSTemplateSet == nil {
		logger.Info("no NSTemplateSet associated with the UserAccount to delete")
		return false, nil
	}
	// Get the NSTemplateSet associated with the UserAccount
	nstmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := r.Client.Get(context.TODO(),
		types.NamespacedName{ // same namespace/name as the UserAccount
			Namespace: userAcc.Namespace,
			Name:      userAcc.Name},
		nstmplSet)
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	if util.IsBeingDeleted(nstmplSet) {
		logger.Info("the NSTemplateSet resource is already being deleted")
		return true, nil
	}
	logger.Info("deleting the NSTemplateSet resource")
	// Delete NSTemplateSet associated with UserAccount
	if err := r.Client.Delete(context.TODO(), nstmplSet); err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	logger.Info("deleted the NSTemplateSet resource")
	return true, nil
}

// wrapErrorWithStatusUpdate wraps the error and update the user account status. If the update failed then logs the error.
func (r *Reconciler) wrapErrorWithStatusUpdate(logger logr.Logger, userAcc *toolchainv1alpha1.UserAccount, statusUpdater func(userAcc *toolchainv1alpha1.UserAccount, message string) error, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(userAcc, err.Error()); err != nil {
		logger.Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *Reconciler) setStatusUserCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateUserReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusIdentityCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateIdentityReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusMappingCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateMappingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusNSTemplateSetCreationFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateNSTemplateSetReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusFromNSTemplateSet(userAcc *toolchainv1alpha1.UserAccount, reason, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
}

func (r *Reconciler) setStatusProvisioning(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountProvisioningReason,
		})
}

func (r *Reconciler) setStatusReady(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.UserAccountProvisionedReason,
		})
}

func (r *Reconciler) setStatusDisabling(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountDisablingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusDisabled(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountDisabledReason,
		})
}

func (r *Reconciler) setStatusUpdating(userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountUpdatingReason,
		})
}

func (r *Reconciler) setStatusUpdateFailed(userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountNSTemplateSetUpdateFailedReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusTerminating(userAcc *toolchainv1alpha1.UserAccount, message string) error {
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
func (r *Reconciler) updateStatusConditions(userAcc *toolchainv1alpha1.UserAccount, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	userAcc.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(userAcc.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.Client.Status().Update(context.TODO(), userAcc)
}

func newUser(userAcc *toolchainv1alpha1.UserAccount, config membercfg.Configuration) *userv1.User {
	user := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: userAcc.Name,
		},
		Identities: []string{ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())},
	}
	return user
}

func newIdentity(userAcc *toolchainv1alpha1.UserAccount, user *userv1.User, config membercfg.Configuration) *userv1.Identity {
	name := ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())
	identity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		ProviderName:     config.Auth().Idp(),
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
		Spec: *userAcc.Spec.NSTemplateSet,
	}
	return nsTmplSet
}

// ToIdentityName converts the given `userID` into an identity
func ToIdentityName(userID string, identityProvider string) string {
	return fmt.Sprintf("%s:%s", identityProvider, userID)
}

func (r *Reconciler) lookupAndDeleteCheUser(logger logr.Logger, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) error {

	// If Che user deletion is not required then just return, this is a way to disable this Che user deletion logic since
	// it's meant to be a temporary measure until Che is updated to handle user deletion on its own
	if !config.Che().IsUserDeletionEnabled() {
		logger.Info("Che user deletion is not enabled, skipping it")
		return nil
	}

	userExists, err := r.CheClient.UserExists(userAcc.Name)
	if err != nil {
		return err
	}

	// If the user doesn't exist then there's nothing left to do.
	if !userExists {
		logger.Info("Che user no longer exists")
		return nil
	}

	// Get the Che user ID to use in the Delete API
	cheUserID, err := r.CheClient.GetUserIDByUsername(userAcc.Name)
	if err != nil {
		return err
	}

	// Delete the Che user. It is common for this call to return an error multiple times before succeeding
	if err := r.CheClient.DeleteUser(cheUserID); err != nil {
		return errs.Wrapf(err, "this error is expected if deletion is still in progress")
	}

	return nil
}
