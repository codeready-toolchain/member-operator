package useraccount

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/satori/go.uuid"
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
	userID := uuid.NewV4().String()

	// given
	userAcc := newUserAccount(username, userID)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name:      getIdentityName(userAcc),
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
	}, Identities: []string{getIdentityName(userAcc)}}

	t.Run("deleted account ignored", func(t *testing.T) {
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
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, &userv1.Identity{})
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create or update user", func(t *testing.T) {
		reconcile := func(r *ReconcileUserAccount, req reconcile.Request, expectedConditions ...toolchainv1alpha1.Condition) {
			//when
			res, err := r.Reconcile(req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			updatedAcc := &toolchainv1alpha1.UserAccount{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
			require.NoError(t, err)
			AssertConditionsMatch(t, updatedAcc.Status.Conditions, expectedConditions...)

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
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, &userv1.Identity{})
			require.Error(t, err)
			assert.True(t, errors.IsNotFound(err))
		}

		t.Run("create", func(t *testing.T) {
			r, req := prepareReconcile(t, username, userAcc)
			reconcile(r, req,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountProvisioning,
					Status: corev1.ConditionTrue,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountUserNotReady,
					Status: corev1.ConditionFalse,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountReady,
					Status: corev1.ConditionFalse,
				})
		})

		t.Run("update", func(t *testing.T) {
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:            username,
				Namespace:       "toolchain-member",
				UID:             userUID,
				OwnerReferences: []metav1.OwnerReference{{UID: userAcc.UID}},
			}}
			r, req := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			reconcile(r, req,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountProvisioning,
					Status: corev1.ConditionTrue,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountUserIdentityMappingNotReady,
					Status: corev1.ConditionFalse,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountReady,
					Status: corev1.ConditionFalse,
				})
		})
	})

	// Second cycle of reconcile. User already created.
	t.Run("create or update identity", func(t *testing.T) {
		reconcile := func(r *ReconcileUserAccount, req reconcile.Request, expectedConditions ...toolchainv1alpha1.Condition) {
			//when
			res, err := r.Reconcile(req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status is now "provisioning"
			updatedAcc := &toolchainv1alpha1.UserAccount{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
			require.NoError(t, err)
			AssertConditionsMatch(t, updatedAcc.Status.Conditions, expectedConditions...)

			// Check the created/updated identity
			identity := &userv1.Identity{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID), identity.Name)
			require.Len(t, identity.GetOwnerReferences(), 1)
			assert.Equal(t, updatedAcc.UID, identity.GetOwnerReferences()[0].UID)

			// Check the user identity mapping
			checkMapping(t, preexistingUser, identity)
		}

		t.Run("create", func(t *testing.T) {
			r, req := prepareReconcile(t, username, userAcc, preexistingUser)
			reconcile(r, req,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountProvisioning,
					Status: corev1.ConditionTrue,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountIdentityNotReady,
					Status: corev1.ConditionFalse,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountReady,
					Status: corev1.ConditionFalse,
				})
		})

		t.Run("update", func(t *testing.T) {
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:            getIdentityName(userAcc),
				Namespace:       "toolchain-member",
				UID:             types.UID(uuid.NewV4().String()),
				OwnerReferences: []metav1.OwnerReference{{UID: userAcc.UID}},
			}}

			r, req := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			reconcile(r, req,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountProvisioning,
					Status: corev1.ConditionTrue,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountUserIdentityMappingNotReady,
					Status: corev1.ConditionFalse,
				},
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.UserAccountReady,
					Status: corev1.ConditionFalse,
				})
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
		AssertConditionsMatch(t, updatedAcc.Status.Conditions,
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
	userID := uuid.NewV4().String()
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	t.Run("status updated", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}
		conditions := []toolchainv1alpha1.Condition{
			{
				Type:   toolchainv1alpha1.UserAccountProvisioning,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   toolchainv1alpha1.UserAccountReady,
				Status: corev1.ConditionTrue,
			},
		}

		// when
		err := reconciler.updateStatusConditions(userAcc, conditions...)

		// then
		require.NoError(t, err)
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: "toolchain-member", Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		AssertConditionsMatch(t, updatedAcc.Status.Conditions, conditions...)
	})

	t.Run("status not updated because not changed", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.UserAccountProvisioning,
			Status: corev1.ConditionFalse,
		}}
		userAcc.Status.Conditions = conditions

		// when
		err := reconciler.updateStatusConditions(userAcc, conditions...)

		// then
		require.NoError(t, err)
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: "toolchain-member", Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		// Status is not updated
		AssertConditionsMatch(t, updatedAcc.Status.Conditions)
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}
		log := logf.Log.WithName("test")

		t.Run("status updated", func(t *testing.T) {
			statusUpdater := func(userAcc *toolchainv1alpha1.UserAccount, message string) error {
				assert.Equal(t, "oopsy woopsy", message)
				return nil
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, userAcc, statusUpdater, errors.NewBadRequest("oopsy woopsy"), "failed to create %s", "user bob")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create user bob: oopsy woopsy", err.Error())
		})

		t.Run("status update failed", func(t *testing.T) {
			statusUpdater := func(userAcc *toolchainv1alpha1.UserAccount, message string) error {
				return errors.NewBadRequest("unable to update status")
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, userAcc, statusUpdater, errors.NewBadRequest("oopsy woopsy"), "failed to create %s", "user bob")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create user bob: oopsy woopsy", err.Error())
		})
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

// AssertConditionsMatch asserts that the specified list A of conditions is equal to specified
// list B of conditions ignoring the order of the elements. We can't use assert.ElementsMatch
// because the LastTransitionTime of the actual conditions can be modified but the conditions
// still should be treated as matched
//TODO move to toolchain-common
func AssertConditionsMatch(t *testing.T, actual []toolchainv1alpha1.Condition, expected ...toolchainv1alpha1.Condition) {
	require.Equal(t, len(expected), len(actual))
	for _, c := range expected {
		AssertContainsCondition(t, actual, c)
	}
}

// AssertContainsCondition asserts that the specified list of conditions contains the specified condition.
// LastTransitionTime is ignored.
//TODO move to toolchain-common
func AssertContainsCondition(t *testing.T, conditions []toolchainv1alpha1.Condition, contains toolchainv1alpha1.Condition) {
	for _, c := range conditions {
		if c.Type == contains.Type {
			assert.Equal(t, contains.Status, c.Status)
			assert.Equal(t, contains.Reason, c.Reason)
			assert.Equal(t, contains.Message, c.Message)
			return
		}
	}
	assert.FailNow(t, fmt.Sprintf("the list of conditions %v doesn't contain the expected condition %v", conditions, contains))
}
