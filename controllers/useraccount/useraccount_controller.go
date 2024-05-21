package useraccount

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commoncontroller "github.com/codeready-toolchain/toolchain-common/controllers"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	membercfg "github.com/codeready-toolchain/toolchain-common/pkg/configuration/memberoperatorconfig"
	commonidentity "github.com/codeready-toolchain/toolchain-common/pkg/identity"

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
		// TODO remove NSTemplateSet watch once appstudio workarounds are removed
		// UserAccount does not contain NSTemplateSet details in its Spec anymore but this controller must still watch NSTemplateSet due to appstudio cases
		// See https://github.com/codeready-toolchain/member-operator/blob/147dbe58f4923b9d936a21995be8b0c084544c6d/controllers/useraccount/useraccount_controller.go#L167-L172
		Watches(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{}).
		Watches(&source.Kind{Type: &userv1.User{}}, mapToOwnerByLabel).
		Watches(&source.Kind{Type: &userv1.Identity{}}, mapToOwnerByLabel).
		Complete(r)
}

// Reconciler reconciles a UserAccount object
type Reconciler struct {
	Client client.Client
	Scheme *runtime.Scheme
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
	err = r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: request.Name}, userAcc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("UserAccount not found")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// If the UserAccount has not been deleted, create or update user and Identity resources.
	// If the UserAccount has been deleted, delete secondary resources identity and user.
	if !util.IsBeingDeleted(userAcc) {
		// Add the finalizer if it is not present
		if err := r.addFinalizer(ctx, userAcc); err != nil {
			return reconcile.Result{}, err
		}
	} else {
		return reconcile.Result{}, r.ensureUserAccountDeletion(ctx, userAcc)
	}

	if !userAcc.Spec.Disabled {
		logger.Info("ensuring user and identity associated with UserAccount")
		if createdOrUpdated, err := r.ensureUserAndIdentity(ctx, userAcc, config); err != nil || createdOrUpdated {
			return reconcile.Result{}, err
		}
	} else {
		logger.Info("Disabling UserAccount")
		if err := r.setStatusDisabling(ctx, userAcc, "deleting user/identity"); err != nil {
			logger.Error(err, "error updating status")
			return reconcile.Result{}, err
		}
		deleted, err := r.deleteIdentityAndUser(ctx, userAcc)
		if err != nil {
			return reconcile.Result{}, r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusDisabling, err, "failed to delete user/identity")
		}

		if !deleted {
			return reconcile.Result{}, r.setStatusDisabled(ctx, userAcc)
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
	return reconcile.Result{}, r.setStatusReady(ctx, userAcc)
}

func (r *Reconciler) ensureUserAndIdentity(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, config membercfg.Configuration) (bool, error) {
	var createdOrUpdated bool
	var user *userv1.User
	var err error
	// create User & Identity resources unless configured otherwise, SkipUserCreation will be set mainly for early appstudio development clusters
	if !config.SkipUserCreation() {
		if user, createdOrUpdated, err = r.ensureUser(ctx, config, userAcc); err != nil || createdOrUpdated {
			return createdOrUpdated, err
		}
		_, createdOrUpdated, err = r.ensureIdentity(ctx, config, userAcc, user)
		return createdOrUpdated, err
	}
	// we don't expect User nor Identity resources to be present for AppStudio tier
	// This can be removed as soon as we don't create UserAccounts in AppStudio environment.
	// Should also remove the NSTemplateSet watch once this is removed.
	deleted, err := r.deleteIdentityAndUser(ctx, userAcc)
	if err != nil {
		return deleted, r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusUserCreationFailed, err, "failed to delete redundant user or identity")
	}
	if deleted {
		if err := r.setStatusProvisioning(ctx, userAcc); err != nil {
			log.FromContext(ctx).Error(err, "error updating status")
			return deleted, err
		}
		return deleted, nil
	}
	return false, nil
}

func (r *Reconciler) ensureUserAccountDeletion(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) error {
	if util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName) {
		logger := log.FromContext(ctx)
		logger.Info("terminating UserAccount")
		// We need to be sure that the status is updated when the UserAccount is deleted.
		// In this case the UserAccountStatus controller updates the MUR on the host cluster
		// In turn, the MUR controller may decide to recreate the UserAccount resource on the
		// member cluster.

		deleted, err := r.deleteIdentityAndUser(ctx, userAcc)
		if err != nil {
			return r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusTerminating, err, "failed to delete user/identity")
		}
		if deleted {
			if err := r.setStatusTerminating(ctx, userAcc, "deleting user/identity"); err != nil {
				logger.Error(err, "error updating status")
				return err
			}
			return nil
		}

		// Remove finalizer from UserAccount
		util.RemoveFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(context.Background(), userAcc); err != nil {
			return r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusTerminating, err, "failed to remove finalizer")
		}
		// no need to update the status of the UserAccount once the finalizer has been removed, since
		// the resource will be deleted
		logger.Info("removed finalizer")
	}
	return nil
}

func (r *Reconciler) ensureUser(ctx context.Context, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, bool, error) {
	user := &userv1.User{}
	logger := log.FromContext(ctx)
	if err := r.Client.Get(ctx, types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new user", "name", userAcc.Name)
			if err := r.setStatusProvisioning(ctx, userAcc); err != nil {
				return nil, false, err
			}
			user = newUser(userAcc, config)
			setLabelsAndAnnotations(user, userAcc, true)
			if err := r.Client.Create(ctx, user); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusUserCreationFailed, err, "failed to create user '%s'", userAcc.Name)
			}
			if err := r.setStatusProvisioning(ctx, userAcc); err != nil {
				return nil, false, err
			}
			logger.Info("user created successfully", "name", userAcc.Name)
			return user, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusUserCreationFailed, err, "failed to get user '%s'", userAcc.Name)
	}
	logger.Info("user already exists")

	// migration step - add missing labels and annotations to existing users if they are not set yet
	err := addLabelsAndAnnotations(ctx, user, r.Client, userAcc, true)
	if err != nil {
		logger.Error(err, "Unable to update labels to add provider")
	}

	// ensure mapping
	expectedIdentities := []string{commonidentity.NewIdentityNamingStandard(userAcc.Spec.PropagatedClaims.Sub, config.Auth().Idp()).IdentityName()}

	// If the OriginalSub property has been set also, then an additional identity is required to be created
	if userAcc.Spec.PropagatedClaims.OriginalSub != "" {
		expectedIdentities = append(expectedIdentities, commonidentity.NewIdentityNamingStandard(userAcc.Spec.PropagatedClaims.OriginalSub, config.Auth().Idp()).IdentityName())
	}

	// Also if the UserID property is set, then an additional identity is required if it is a different value to the Sub
	if userAcc.Spec.PropagatedClaims.UserID != "" && userAcc.Spec.PropagatedClaims.UserID != userAcc.Spec.PropagatedClaims.Sub {
		expectedIdentities = append(expectedIdentities, commonidentity.NewIdentityNamingStandard(
			userAcc.Spec.PropagatedClaims.UserID, config.Auth().Idp()).IdentityName())
	}

	stringSlicesEqual := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i, v := range a {
			if v != b[i] {
				return false
			}
		}
		return true
	}

	if !stringSlicesEqual(expectedIdentities, user.Identities) {
		logger.Info("user is missing a reference to identity; updating the reference", "name", userAcc.Name)
		if err := r.setStatusProvisioning(ctx, userAcc); err != nil {
			return nil, false, err
		}
		user.Identities = expectedIdentities
		if err := r.Client.Update(ctx, user); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAcc, r.setStatusMappingCreationFailed, err, "failed to update user '%s'", userAcc.Name)
		}
		if err := r.setStatusProvisioning(ctx, userAcc); err != nil {
			return nil, false, err
		}
		logger.Info("user updated successfully")
		return user, true, nil
	}

	return user, false, nil
}

func (r *Reconciler) ensureIdentity(ctx context.Context, config membercfg.Configuration, userAcc *toolchainv1alpha1.UserAccount, user *userv1.User) (*userv1.Identity, bool, error) {
	identity, createdOrUpdated, err := r.loadIdentityAndEnsureMapping(ctx, config, userAcc.Spec.PropagatedClaims.Sub, userAcc, user)
	if createdOrUpdated || err != nil {
		return nil, createdOrUpdated, err
	}

	// Check if the OriginalSub property is set, and if it is create additional identity/s as required
	if userAcc.Spec.PropagatedClaims.OriginalSub != "" {
		// Encoded the OriginalSub as an unpadded Base64 value
		_, createdOrUpdated, err := r.loadIdentityAndEnsureMapping(ctx, config, userAcc.Spec.PropagatedClaims.OriginalSub, userAcc, user)
		if createdOrUpdated || err != nil {
			return nil, createdOrUpdated, err
		}
	}

	// Check if the userID property is set, and if it is create an additional identity if it is a different value.
	// So we always have an identity with the name generated out of SSO UserID (stored in the userID property) in
	// addition to the identity with the name generated out of the SSO Token sub claim (stored as UserAccount.Spec.PropagatedClaims.Sub).
	// This additional Identity is not created if the SSO UserID == SSO Token sub claim.
	if userAcc.Spec.PropagatedClaims.UserID != "" && userAcc.Spec.PropagatedClaims.UserID != userAcc.Spec.PropagatedClaims.Sub {
		_, createdOrUpdated, err := r.loadIdentityAndEnsureMapping(ctx, config, userAcc.Spec.PropagatedClaims.UserID, userAcc, user)
		if createdOrUpdated || err != nil {
			return nil, createdOrUpdated, err
		}
	}

	return identity, false, nil
}

func (r *Reconciler) loadIdentityAndEnsureMapping(ctx context.Context, config membercfg.Configuration, username string,
	userAccount *toolchainv1alpha1.UserAccount, user *userv1.User) (*userv1.Identity, bool, error) {

	ins := commonidentity.NewIdentityNamingStandard(username, config.Auth().Idp())

	identity := &userv1.Identity{}

	logger := log.FromContext(ctx)
	if err := r.Client.Get(ctx, types.NamespacedName{Name: ins.IdentityName()}, identity); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("creating a new identity")
			if err := r.setStatusProvisioning(ctx, userAccount); err != nil {
				return nil, false, err
			}
			identity = newIdentity(user)
			commonidentity.NewIdentityNamingStandard(username, config.Auth().Idp()).ApplyToIdentity(identity)

			setLabelsAndAnnotations(identity, userAccount, false)
			if err := r.Client.Create(ctx, identity); err != nil {
				return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAccount, r.setStatusIdentityCreationFailed, err, "failed to create identity '%s'", ins.IdentityName())
			}
			if err := r.setStatusProvisioning(ctx, userAccount); err != nil {
				return nil, false, err
			}
			logger.Info("identity created successfully")
			return identity, true, nil
		}
		return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAccount, r.setStatusIdentityCreationFailed, err, "failed to get identity '%s'", ins.IdentityName())
	}
	logger.Info("identity already exists")

	// migration step - add missing labels and annotations to existing identity if they are not set yet
	err := addLabelsAndAnnotations(ctx, identity, r.Client, userAccount, false)
	if err != nil {
		logger.Error(err, "Unable to update label to add provider")
	}

	// ensure mapping
	if identity.User.Name != user.Name || identity.User.UID != user.UID {
		logger.Info("identity is missing a reference to user; updating the reference", "identity", ins.IdentityName(), "user", user.Name)
		if err := r.setStatusProvisioning(ctx, userAccount); err != nil {
			return nil, false, err
		}
		identity.User = corev1.ObjectReference{
			Name: user.Name,
			UID:  user.UID,
		}
		if err := r.Client.Update(ctx, identity); err != nil {
			return nil, false, r.wrapErrorWithStatusUpdate(ctx, userAccount, r.setStatusMappingCreationFailed, err, "failed to update identity '%s'", ins.IdentityName())
		}
		if err := r.setStatusProvisioning(ctx, userAccount); err != nil {
			return nil, false, err
		}
		logger.Info("identity updated successfully")
		return identity, true, nil
	}

	return identity, false, nil
}

func setLabelsAndAnnotations(object metav1.Object, userAcc *toolchainv1alpha1.UserAccount, isUserResource bool) bool {
	var changed bool
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	if _, exists := labels[toolchainv1alpha1.ProviderLabelKey]; !exists {
		labels[toolchainv1alpha1.ProviderLabelKey] = toolchainv1alpha1.ProviderLabelValue
		object.SetLabels(labels)
		changed = true
	}

	if _, exists := labels[toolchainv1alpha1.OwnerLabelKey]; !exists {
		labels[toolchainv1alpha1.OwnerLabelKey] = userAcc.Name
		object.SetLabels(labels)
		changed = true
	}

	if isUserResource {
		annotations := object.GetAnnotations()
		if _, exists := annotations[toolchainv1alpha1.EmailUserAnnotationKey]; !exists {
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[toolchainv1alpha1.EmailUserAnnotationKey] = userAcc.Spec.PropagatedClaims.Email
			object.SetAnnotations(annotations)
			changed = true
		}

		annotations = object.GetAnnotations()
		set := false
		if userAcc.Spec.PropagatedClaims.UserID != "" && userAcc.Spec.PropagatedClaims.AccountID != "" {
			set = true
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[toolchainv1alpha1.UserIDUserAnnotationKey] = userAcc.Spec.PropagatedClaims.UserID
			annotations[toolchainv1alpha1.AccountIDUserAnnotationKey] = userAcc.Spec.PropagatedClaims.AccountID
			object.SetAnnotations(annotations)
			changed = true

		}

		// Delete the UserID and AccountID annotations if they don't exist in the UserAccount
		if !set && object.GetAnnotations() != nil {
			annotations = object.GetAnnotations()
			delete(annotations, toolchainv1alpha1.UserIDUserAnnotationKey)
			delete(annotations, toolchainv1alpha1.AccountIDUserAnnotationKey)
			object.SetAnnotations(annotations)
			changed = true
		}
	}
	return changed
}

func addLabelsAndAnnotations(ctx context.Context, object client.Object, cl client.Client, userAcc *toolchainv1alpha1.UserAccount, isUserResource bool) error {
	if setLabelsAndAnnotations(object, userAcc, isUserResource) {
		return cl.Update(ctx, object)
	}
	return nil
}

// setFinalizers sets the finalizers for UserAccount
func (r *Reconciler) addFinalizer(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) error {
	// Add the finalizer if it is not present
	if !util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName) {
		log.FromContext(ctx).Info("adding finalizer on UserAccount")
		util.AddFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
		if err := r.Client.Update(ctx, userAcc); err != nil {
			return err
		}
	}

	return nil
}

// deleteIdentityAndUser deletes the identity and user.
// Returns bool and error indicating that whether the user/identity were deleted.
func (r *Reconciler) deleteIdentityAndUser(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	if deleted, err := r.deleteIdentity(ctx, userAcc); err != nil || deleted {
		return deleted, err
	}
	if deleted, err := r.deleteUser(ctx, userAcc); err != nil || deleted {
		return deleted, err
	}
	return false, nil
}

// deleteUser deletes the user resources associated with the specified UserAccount.
// Returns `true` if the users were deleted, `false` otherwise, with the underlying error
// if the user existed and something wrong happened. If the users don't exist,
// this func returns `false, nil`
func (r *Reconciler) deleteUser(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	userList, err := getUsersByOwnerName(ctx, r.Client, userAcc.Name)
	if err != nil {
		return false, err
	}

	if len(userList) == 0 {
		return false, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("deleting the User resources")

	//get the UID before deleting the user
	// TO DO: this is a workaround for breaking change introduced by console with upgrade to 4.15. This remove resources created for users logging in from OIDC which don't have owner references after 4.15
	uid := userList[0].UID
	logger.Info(fmt.Sprintf("checking for user settings resources for the user with UID [%s] to be deleted", uid))
	if err := r.deleteUserResources(ctx, string(uid)); err != nil {
		return false, err
	}
	// Delete User associated with UserAccount
	if err := r.Client.Delete(ctx, &userList[0]); err != nil {
		return false, err
	}
	// Return here, as deleting the user should cause another reconcile of the UserAccount
	logger.Info(fmt.Sprintf("deleted User resource [%s]", userList[0].Name))
	return true, nil
}

// Returns `true` if the associated resources (configMap, role and role-binding) created for a user by console are deleted, `false` otherwise.
// This function only looks for these resources in the namespace - openshift-console-user-settings
func (r *Reconciler) deleteUserResources(ctx context.Context, userUID string) error {

	// Users which were created in the cluster with the OCP versions which includes https://issues.redhat.com/browse/OCPBUGS-32321 fix
	// will have a label which will help to map the User to the User settings resources.

	// Users created before that won't have that label, and we have to rely on the name of the resource being of type `user-settings-<UID>`,
	// where <UID> is the User's UID.
	// delete ConfigMap, Role and RoleBinding
	if _, err := deleteConfigMap(ctx, r.Client, userUID); err != nil {
		return err
	}
	if _, err := deleteRole(ctx, r.Client, userUID); err != nil {
		return err
	}
	if _, err := deleteRoleBinding(ctx, r.Client, userUID); err != nil {
		return err
	}
	return nil
}

// deleteIdentity deletes the Identity resources owned by the specified UserAccount.
// Returns `true` if one or more identities were deleted, `false` otherwise, with the underlying error
// if the identity existed and something wrong happened.
func (r *Reconciler) deleteIdentity(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) (bool, error) {
	identityList := &userv1.IdentityList{}
	err := r.Client.List(ctx, identityList, listByOwnerLabel(userAcc.Name))
	if err != nil {
		return false, err
	}

	if len(identityList.Items) == 0 {
		return false, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("deleting Identity resources")

	// Delete first Identity in the list associated with UserAccount
	if err := r.Client.Delete(ctx, &identityList.Items[0]); err != nil {
		return false, err
	}
	// Return here, as deleting the identity should cause another reconcile of the UserAccount
	logger.Info(fmt.Sprintf("deleted Identity resource [%s]", identityList.Items[0].Name))
	return true, nil
}

// wrapErrorWithStatusUpdate wraps the error and update the user account status. If the update failed then logs the error.
func (r *Reconciler) wrapErrorWithStatusUpdate(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, statusUpdater func(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error, err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	if err := statusUpdater(ctx, userAcc, err.Error()); err != nil {
		log.FromContext(ctx).Error(err, "status update failed")
	}
	return errs.Wrapf(err, format, args...)
}

func (r *Reconciler) setStatusUserCreationFailed(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateUserReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusIdentityCreationFailed(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateIdentityReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusMappingCreationFailed(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountUnableToCreateMappingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusProvisioning(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountProvisioningReason,
		})
}

func (r *Reconciler) setStatusReady(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: toolchainv1alpha1.UserAccountProvisionedReason,
		})
}

func (r *Reconciler) setStatusDisabling(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountDisablingReason,
			Message: message,
		})
}

func (r *Reconciler) setStatusDisabled(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
			Reason: toolchainv1alpha1.UserAccountDisabledReason,
		})
}

func (r *Reconciler) setStatusTerminating(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, message string) error {
	return r.updateStatusConditions(
		ctx,
		userAcc,
		toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  toolchainv1alpha1.UserAccountTerminatingReason,
			Message: message,
		})
}

// updateStatusConditions updates user account status conditions with the new conditions
func (r *Reconciler) updateStatusConditions(ctx context.Context, userAcc *toolchainv1alpha1.UserAccount, newConditions ...toolchainv1alpha1.Condition) error {
	var updated bool
	userAcc.Status.Conditions, updated = condition.AddOrUpdateStatusConditions(userAcc.Status.Conditions, newConditions...)
	if !updated {
		// Nothing changed
		return nil
	}
	return r.Client.Status().Update(ctx, userAcc)
}

func newUser(userAcc *toolchainv1alpha1.UserAccount, config membercfg.Configuration) *userv1.User {
	identities := []string{commonidentity.NewIdentityNamingStandard(userAcc.Spec.PropagatedClaims.Sub, config.Auth().Idp()).IdentityName()}
	if userAcc.Spec.PropagatedClaims.OriginalSub != "" {
		identities = append(identities, commonidentity.NewIdentityNamingStandard(userAcc.Spec.PropagatedClaims.OriginalSub, config.Auth().Idp()).IdentityName())
	}
	if userAcc.Spec.PropagatedClaims.UserID != "" && userAcc.Spec.PropagatedClaims.UserID != userAcc.Spec.PropagatedClaims.Sub {
		identities = append(identities, commonidentity.NewIdentityNamingStandard(userAcc.Spec.PropagatedClaims.UserID, config.Auth().Idp()).IdentityName())
	}

	user := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: userAcc.Name,
		},
		Identities: identities,
	}

	return user
}
func newIdentity(user *userv1.User) *userv1.Identity {
	identity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{},
		User: corev1.ObjectReference{
			Name: user.Name,
			UID:  user.UID,
		},
	}
	return identity
}

// ToIdentityName converts the given `userID` into an identity
func ToIdentityName(userID string, identityProvider string) string {
	return fmt.Sprintf("%s:%s", identityProvider, userID)
}

// listByOwnerLabel returns runtimeclient.ListOption that filters by label toolchain.dev.openshift.com/owner equal to the given owner name
func listByOwnerLabel(owner string) client.ListOption {
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: owner}
	return client.MatchingLabels(labels)
}

// getUsersByOwnerName gets the user resources by matching owner label.
func getUsersByOwnerName(ctx context.Context, cl client.Client, owner string) ([]userv1.User, error) {
	userList := &userv1.UserList{}
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: owner}
	err := cl.List(ctx, userList, client.MatchingLabels(labels))
	if err != nil {
		return []userv1.User{}, err
	}
	return userList.Items, nil
}
