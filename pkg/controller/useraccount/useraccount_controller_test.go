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
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	// given
	userAcc := newUserAccount(username, userID)

	// Objects to track in the fake client.
	objs := []runtime.Object{userAcc}

	// Register operator types with the runtime scheme.
	s.AddKnownTypes(toolchainv1alpha1.SchemeGroupVersion, userAcc)

	client := fake.NewFakeClient(objs...)
	r := &ReconcileUserAccount{
		client: client,
		scheme: s,
	}

	req := newReconcileRequest(username)
	identity := &userv1.Identity{}
	user := &userv1.User{}

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

		// Check the created user
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)
		assert.Equal(t, userAcc.Name, user.Name)

		// Check the identity is not created yet
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

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

		// Check the created identity
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID), identity.Name)
	})

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

		// Check the created user identity mapping
		mapping := newMapping(user, identity)
		err = r.client.Create(context.TODO(), mapping)
		require.Error(t, err)
		assert.True(t, errors.IsAlreadyExists(err))
	})

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
	})
}

func TestEnsureIdentity(t *testing.T) {
	//logf.SetLogger(logf.ZapLogger(true))
	//username := "johnsmith"
	//userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	//s := scheme.Scheme
	//err := apis.AddToScheme(s)
	//require.NoError(t, err)
	//
	//t.Run("create_identity", func(t *testing.T) {
	//	userAcc := newUserAccount(username, userID)
	//	client := fake.NewFakeClient()
	//	reconciler := &ReconcileUserAccount{
	//		client: client,
	//		scheme: s,
	//	}
	//
	//	newIdentity, created, err := reconciler.ensureIdentity(userAcc)
	//	assert.NoError(t, err)
	//	assert.True(t, created)
	//	assert.NotNil(t, newIdentity)
	//})
	//
	//t.Run("get_identity", func(t *testing.T) {
	//	userAcc := newUserAccount(username, userID)
	//	identity := newIdentity(userAcc)
	//	client := fake.NewFakeClient(identity)
	//	reconciler := &ReconcileUserAccount{
	//		client: client,
	//		scheme: s,
	//	}
	//
	//	newIdentity, created, err := reconciler.ensureIdentity(userAcc)
	//	assert.NoError(t, err)
	//	assert.False(t, created)
	//	assert.NotNil(t, newIdentity)
	//})
}

func TestEnsureMapping(t *testing.T) {
	//logf.SetLogger(logf.ZapLogger(true))
	//username := "johnsmith"
	//userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	//s := scheme.Scheme
	//err := apis.AddToScheme(s)
	//require.NoError(t, err)
	//
	//t.Run("create_mapping_true", func(t *testing.T) {
	//	userAcc := newUserAccount(username, userID)
	//	user := newUser(userAcc)
	//	identity := newIdentity(userAcc)
	//	client := fake.NewFakeClient(user, identity)
	//	reconciler := &ReconcileUserAccount{
	//		client: client,
	//		scheme: s,
	//	}
	//
	//	created, err := reconciler.ensureMapping(userAcc, user, identity)
	//	assert.NoError(t, err)
	//	assert.True(t, created)
	//})
	//
	//t.Run("create_mapping_false", func(t *testing.T) {
	//	userAcc := newUserAccount(username, userID)
	//	user := newUser(userAcc)
	//	identity := newIdentity(userAcc)
	//	mapping := newMapping(user, identity)
	//	client := fake.NewFakeClient(user, identity, mapping)
	//
	//	reconciler := &ReconcileUserAccount{
	//		client: client,
	//		scheme: s,
	//	}
	//	created, err := reconciler.ensureMapping(userAcc, user, identity)
	//	assert.NoError(t, err)
	//	assert.False(t, created)
	//})
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
