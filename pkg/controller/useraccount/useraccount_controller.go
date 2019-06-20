package useraccount

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	userv1 "github.com/openshift/api/user/v1"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileUserAccount{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
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

	var user *userv1.User
	if user, err = r.ensureUser(userAcc); err != nil {
		return reconcile.Result{}, err
	}

	var identity *userv1.Identity
	if identity, err = r.ensureIdentity(userAcc); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.ensureMapping(user, identity); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileUserAccount) ensureUser(userAcc *toolchainv1alpha1.UserAccount) (*userv1.User, error) {
	user := &userv1.User{}
	if err := r.client.Get(context.Background(), types.NamespacedName{Name: userAcc.Name}, user); err != nil {
		if errors.IsNotFound(err) {
			log.Info("creating a new user", "name", userAcc.Name)
			user = &userv1.User{
				ObjectMeta: metav1.ObjectMeta{
					Name: userAcc.Name,
				},
			}
			if err := r.client.Create(context.Background(), user); err != nil {
				return nil, errs.Wrapf(err, "failed to create user '%s'", userAcc.Name)
			}
			log.Info("user created successfully", "name", userAcc.Name)
			return user, nil
		}
		return nil, errs.Wrapf(err, "failed to get user '%s'", userAcc.Name)
	}
	log.Info("user already exists", "name", userAcc.Name)
	return user, nil
}

func (r *ReconcileUserAccount) ensureIdentity(userAcc *toolchainv1alpha1.UserAccount) (*userv1.Identity, error) {
	name := fmt.Sprintf("%s:%s", getIdentityProviderName(), userAcc.Spec.UserID)
	identity := &userv1.Identity{}
	if err := r.client.Get(context.Background(), types.NamespacedName{Name: name}, identity); err != nil {
		if errors.IsNotFound(err) {
			log.Info("creating a new identity", "name", name)
			identity = &userv1.Identity{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				ProviderName:     getIdentityProviderName(),
				ProviderUserName: userAcc.Spec.UserID,
			}
			if err := r.client.Create(context.Background(), identity); err != nil {
				return nil, errs.Wrapf(err, "failed to create identity '%s'", name)
			}
			log.Info("identity created successfully", "name", name)
			return identity, nil
		}
		return nil, errs.Wrapf(err, "failed to get identity '%s'", name)
	}
	log.Info("identity already exists", "name", name)
	return identity, nil
}

func (r *ReconcileUserAccount) ensureMapping(user *userv1.User, identity *userv1.Identity) error {
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
	if err := r.client.Create(context.Background(), mapping); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("user-identity mapping already exists", "name", name)
			return nil
		}
		return errs.Wrapf(err, "failed to create user-identity mapping '%s'", name)
	}
	log.Info("user-identity mapping created successfully", "name", name)
	return nil
}

// TODO check how to get IdP name
func getIdentityProviderName() string {
	return "gh_provider"
}
