package useraccount

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestReconcileOK(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	// given
	userAcc := newUserAccount(username, userID)

	client := fake.NewFakeClient(userAcc)
	r := &ReconcileUserAccount{
		client: client,
		scheme: s,
	}

	req := newReconcileRequest(username)
	identity := &userv1.Identity{}
	user := &userv1.User{}

	t.Run("deleted_account_ignored", func(t *testing.T) {
		// given
		// No user account exists
		client := fake.NewFakeClient()
		r := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check the user is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))

		// Check the identity is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create_user", func(t *testing.T) {
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check that the user account status has been updated
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, updatedAcc.Status.Status)
		assert.Empty(t, updatedAcc.Status.Error)

		// Check the created user
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)
		assert.Equal(t, userAcc.Name, user.Name)
		assert.Len(t, user.GetOwnerReferences(), 1)
		assert.Equal(t, updatedAcc.UID, user.GetOwnerReferences()[0].UID)

		// Check the identity is not created yet
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	// Second cycle of reconcile. User already created.
	t.Run("create_identity", func(t *testing.T) {
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check that the user account status is still "provisioning"
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, updatedAcc.Status.Status)
		assert.Empty(t, updatedAcc.Status.Error)

		// Check the created identity
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID), identity.Name)
		assert.Len(t, identity.GetOwnerReferences(), 1)
		assert.Equal(t, updatedAcc.UID, identity.GetOwnerReferences()[0].UID)
	})

	// Third cycle of reconcile. User and Identity already created.
	t.Run("create_user_identity_mapping", func(t *testing.T) {
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check that the user account status is still "provisioning"
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, updatedAcc.Status.Status)
		assert.Empty(t, updatedAcc.Status.Error)

		// Check the created user identity mapping
		mapping := newMapping(user, identity)
		err = r.client.Create(context.TODO(), mapping)
		require.Error(t, err)
		assert.True(t, errors.IsAlreadyExists(err))
	})

	// Last cycle of reconcile. User, Identity and UserIdentityMapping created/updated.
	t.Run("provisioned", func(t *testing.T) {
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check that the user account status is now "provisioned"
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioned, updatedAcc.Status.Status)
		assert.Empty(t, updatedAcc.Status.Error)
	})
}

func TestUpdateStatus(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	t.Run("status_ok", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		err := reconciler.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, "")

		assert.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, userAcc.Status.Status)
		assert.Equal(t, "", userAcc.Status.Error)
	})

	t.Run("status_error", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		err := reconciler.updateStatus(userAcc, toolchainv1alpha1.StatusProvisioning, "some error")

		assert.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, userAcc.Status.Status)
		assert.Equal(t, "some error", userAcc.Status.Error)
	})

	t.Run("status_error_wrapped", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		log := logf.Log.WithName("test")
		err := reconciler.wrapErrorWithStatusUpdate(log, userAcc, errors.NewBadRequest("oopsy woopsy"), "failed to create %s", "user bob")

		require.Error(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, userAcc.Status.Status)
		assert.Equal(t, "oopsy woopsy", userAcc.Status.Error)
		assert.Equal(t, "failed to create user bob: oopsy woopsy", err.Error())
	})

	t.Run("status_error_reset", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Status.Error = "this error should be reset to an empty string"

		client := fake.NewFakeClient(userAcc)
		r := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		req := newReconcileRequest(username)

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		// Check updated status
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		assert.Equal(t, toolchainv1alpha1.StatusProvisioning, updatedAcc.Status.Status)
		assert.Empty(t, updatedAcc.Status.Error)
	})
}

func newUserAccount(userName, userID string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: "toolchain-member",
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID: userID,
		},
	}
	return userAcc
}

func newReconcileRequest(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: "toolchain-member",
		},
	}
}
