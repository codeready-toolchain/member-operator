package useraccount

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	userv1 "github.com/openshift/api/user/v1"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

	// given
	userAcc := newUserAccount(username, userID)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name:      ToIdentityName(userAcc.Spec.UserID),
		Namespace: "toolchain-member",
		UID:       types.UID(username + "identity"),
	}, User: corev1.ObjectReference{
		Name: username,
		UID:  userUID,
	}}
	preexistingUser := &userv1.User{ObjectMeta: metav1.ObjectMeta{
		Name:            username,
		Namespace:       "toolchain-member",
		UID:             userUID,
		OwnerReferences: []metav1.OwnerReference{},
	}, Identities: []string{ToIdentityName(userAcc.Spec.UserID)}}

	t.Run("deleted_account_ignored", func(t *testing.T) {
		// given
		// No user account exists
		r, req := prepareReconcile(t, username)
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check the user is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, &userv1.User{})
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))

		// Check the identity is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID)}, &userv1.Identity{})
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create_or_update_user", func(t *testing.T) {
		reconcile := func(r *ReconcileUserAccount, req reconcile.Request) {
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

			// Check the created/updated user
			user := &userv1.User{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
			require.NoError(t, err)
			assert.Equal(t, userAcc.Name, user.Name)
			require.Len(t, user.GetOwnerReferences(), 1)
			assert.Equal(t, updatedAcc.UID, user.GetOwnerReferences()[0].UID)

			// Check the user identity mapping
			user.UID = preexistingUser.UID // we have to set UID for the obtained user because the fake client doesn't set it
			checkMapping(t, user, preexistingIdentity)

			// Check the identity is not created yet
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID)}, &userv1.Identity{})
			require.Error(t, err)
			assert.True(t, errors.IsNotFound(err))
		}

		t.Run("create", func(t *testing.T) {
			r, req := prepareReconcile(t, username, userAcc)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:            username,
				Namespace:       "toolchain-member",
				UID:             userUID,
				OwnerReferences: []metav1.OwnerReference{{UID: userAcc.UID}},
			}}
			r, req := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			reconcile(r, req)
		})
	})

	// Second cycle of reconcile. User already created.
	t.Run("create_or_update_identity", func(t *testing.T) {
		reconcile := func(r *ReconcileUserAccount, req reconcile.Request) {
			//when
			res, err := r.Reconcile(req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status is now "provisioning"
			updatedAcc := &toolchainv1alpha1.UserAccount{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
			require.NoError(t, err)
			assert.Equal(t, toolchainv1alpha1.StatusProvisioning, updatedAcc.Status.Status)
			assert.Empty(t, updatedAcc.Status.Error)

			// Check the created/updated identity
			identity := &userv1.Identity{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID)}, identity)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID), identity.Name)
			require.Len(t, identity.GetOwnerReferences(), 1)
			assert.Equal(t, updatedAcc.UID, identity.GetOwnerReferences()[0].UID)

			// Check the user identity mapping
			checkMapping(t, preexistingUser, identity)
		}

		t.Run("create", func(t *testing.T) {
			r, req := prepareReconcile(t, username, userAcc, preexistingUser)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:            ToIdentityName(userAcc.Spec.UserID),
				Namespace:       "toolchain-member",
				UID:             types.UID(uuid.NewV4().String()),
				OwnerReferences: []metav1.OwnerReference{{UID: userAcc.UID}},
			}}

			r, req := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			reconcile(r, req)
		})

	})

	// Last cycle of reconcile. User, Identity created/updated.
	t.Run("provisioned", func(t *testing.T) {
		// given
		r, req := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

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

func prepareReconcile(t *testing.T, username string, initObjs ...runtime.Object) (*ReconcileUserAccount, reconcile.Request) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	client := fake.NewFakeClientWithScheme(s, initObjs...)

	r := &ReconcileUserAccount{
		client: client,
		scheme: s,
	}
	return r, newReconcileRequest(username)
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
			UID:       types.UID(uuid.NewV4().String()),
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

func checkMapping(t *testing.T, user *userv1.User, identity *userv1.Identity) {
	assert.Equal(t, user.Name, identity.User.Name)
	assert.Equal(t, user.UID, identity.User.UID)
	require.Len(t, user.Identities, 1)
	assert.Equal(t, identity.Name, user.Identities[0])
}
