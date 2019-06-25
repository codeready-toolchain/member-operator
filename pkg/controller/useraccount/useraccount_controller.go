package useraccount

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	userv1 "github.com/openshift/api/user/v1"
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
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.UserAccount{}}, &handler.EnqueueRequestForObject{})
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
	if user, created, err = r.ensureUser(userAcc); err != nil || created {
		return reconcile.Result{}, err
	}

	var identity *userv1.Identity
	if identity, created, err = r.ensureIdentity(userAcc); err != nil || created {
		return reconcile.Result{}, err
	}

	if created, err = r.ensureMapping(userAcc, user, identity); err != nil || created {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileUserAccount) ensureUser(userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, bool, error) {
	user := &userv1.User{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("creating a new user", "name", userAcc.Name)
			user = newUser(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, user, r.scheme); err != nil {
				return nil, false, err
			}
			if err := r.client.Create(context.TODO(), user); err != nil {
				return nil, false, errs.Wrapf(err, "failed to create user '%s'", userAcc.Name)
			}
			log.Info("user created successfully", "name", userAcc.Name)
			return user, true, nil
		}
		return nil, false, errs.Wrapf(err, "failed to get user '%s'", userAcc.Name)
	}
	log.Info("user already exists", "name", userAcc.Name)
	return user, false, nil
}

func (r *ReconcileUserAccount) ensureIdentity(userAcc *toolchainv1alpha1.UserAccount) (*userv1.Identity, bool, error) {
	name := getIdentityName(userAcc)
	identity := &userv1.Identity{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: name}, identity); err != nil {
		if errors.IsNotFound(err) {
			log.Info("creating a new identity", "name", name)
			identity = newIdentity(userAcc)
			if err := controllerutil.SetControllerReference(userAcc, identity, r.scheme); err != nil {
				return nil, false, err
			}
			if err := r.client.Create(context.TODO(), identity); err != nil {
				return nil, false, errs.Wrapf(err, "failed to create identity '%s'", name)
			}
			log.Info("identity created successfully", "name", name)
			return identity, true, nil
		}
		return nil, false, errs.Wrapf(err, "failed to get identity '%s'", name)
	}
	log.Info("identity already exists", "name", name)
	return identity, false, nil
}

func (r *ReconcileUserAccount) ensureMapping(userAcc *toolchainv1alpha1.UserAccount, user *userv1.User, identity *userv1.Identity) (bool, error) {
	mapping := newMapping(user, identity)
	name := mapping.Name
	if err := controllerutil.SetControllerReference(userAcc, mapping, r.scheme); err != nil {
		return false, err
	}

	if err := r.client.Create(context.TODO(), mapping); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("user-identity mapping already exists", "name", name)
			return false, nil
		}
		return false, errs.Wrapf(err, "failed to create user-identity mapping '%s'", name)
	}
	log.Info("user-identity mapping created successfully", "name", name)
	return true, nil
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
	name := getIdentityName(userAcc)
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

func getIdentityName(userAcc *toolchainv1alpha1.UserAccount) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID)
}
