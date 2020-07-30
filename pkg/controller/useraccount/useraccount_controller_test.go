package useraccount

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test/useraccount"

	userv1 "github.com/openshift/api/user/v1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestReconcile(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := uuid.NewV4().String()
	config, err := configuration.LoadConfig(test.NewFakeClient(t))
	require.NoError(t, err)

	// given
	userAcc := newUserAccount(username, userID, false)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name: ToIdentityName(userAcc.Spec.UserID, config),
		UID:  types.UID(username + "identity"),
	}, User: corev1.ObjectReference{
		Name: username,
		UID:  userUID,
	}}
	preexistingUser := &userv1.User{ObjectMeta: metav1.ObjectMeta{
		Name:   username,
		UID:    userUID,
		Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
	}, Identities: []string{ToIdentityName(userAcc.Spec.UserID, config)}}
	preexistingNsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userAcc.Name,
			Namespace: test.MemberOperatorNs,
		},
		Spec: newNSTmplSetSpec(),
		Status: toolchainv1alpha1.NSTemplateSetStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{Type: toolchainv1alpha1.ConditionReady, Status: corev1.ConditionTrue},
			},
		},
	}

	t.Run("deleted account ignored", func(t *testing.T) {
		// given
		// No user account exists
		r, req, _ := prepareReconcile(t, username)
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check the user is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, &userv1.User{})
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check the identity is not created
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, &userv1.Identity{})
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check the NSTmplSet is not created
		AssertThatNSTemplateSet(t, req.Namespace, userAcc.Name, r.client).
			DoesNotExist()
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create or update user OK", func(t *testing.T) {
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
			test.AssertConditionsMatch(t, updatedAcc.Status.Conditions,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionFalse,
					Reason: "Provisioning",
				})

			// Check the created/updated user
			user := &userv1.User{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
			require.NoError(t, err)
			assert.Equal(t, userAcc.Name, user.Name)
			require.Equal(t, userAcc.Name, user.Labels["toolchain.dev.openshift.com/owner"])
			assert.Empty(t, user.OwnerReferences) // User has no explicit owner reference.

			// Check the user identity mapping
			user.UID = preexistingUser.UID // we have to set UID for the obtained user because the fake client doesn't set it
			checkMapping(t, user, preexistingIdentity)

			// Check the identity is not created yet
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, &userv1.Identity{})
			require.Error(t, err)
			assert.True(t, apierros.IsNotFound(err))

			// Check the NSTmplSet is not created yet
			AssertThatNSTemplateSet(t, req.Namespace, userAcc.Name, r.client).
				DoesNotExist()
		}

		t.Run("create", func(t *testing.T) {
			r, req, _ := prepareReconcile(t, username, userAcc)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:   username,
				UID:    userUID,
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
			}}
			r, req, _ := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update user failed", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, username, userAcc)
			fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create user")
			}

			//when
			res, err := r.Reconcile(req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to create user '%s': unable to create user", username))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(failed("UnableToCreateUser", "unable to create user"))
		})
		t.Run("update", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithFinalizer(username, userID)
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:   username,
				UID:    userUID,
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
			}}
			r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update user")
			}

			//when
			res, err := r.Reconcile(req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to update user '%s': unable to update user", username))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(failed("UnableToCreateMapping", "unable to update user"))
		})
	})

	// Second cycle of reconcile. User already created.
	t.Run("create or update identity OK", func(t *testing.T) {
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
			test.AssertConditionsMatch(t, updatedAcc.Status.Conditions,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionFalse,
					Reason: "Provisioning",
				})

			// Check the created/updated identity
			identity := &userv1.Identity{}
			err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, identity)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%s:%s", r.config.GetIdP(), userAcc.Spec.UserID), identity.Name)
			require.Equal(t, userAcc.Name, identity.Labels["toolchain.dev.openshift.com/owner"])
			assert.Empty(t, identity.OwnerReferences) // Identity has no explicit owner reference.

			// Check the user identity mapping
			checkMapping(t, preexistingUser, identity)
		}

		t.Run("create", func(t *testing.T) {
			r, req, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:   ToIdentityName(userAcc.Spec.UserID, config),
				UID:    types.UID(uuid.NewV4().String()),
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name},
			}}

			r, req, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update identity failed", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser)
			fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create identity")
			}

			//when
			res, err := r.Reconcile(req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to create identity '%s': unable to create identity", ToIdentityName(userAcc.Spec.UserID, config)))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(failed("UnableToCreateIdentity", "unable to create identity"))
		})
		t.Run("update", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithFinalizer(username, userID)
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:   ToIdentityName(userAcc.Spec.UserID, config),
				UID:    types.UID(uuid.NewV4().String()),
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name},
			}}
			r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update identity")
			}

			//when
			res, err := r.Reconcile(req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to update identity '%s': unable to update identity", preexistingIdentityWithNoMapping.Name))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(failed("UnableToCreateMapping", "unable to update identity"))
		})
	})

	t.Run("create nstmplset OK", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			// given
			r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

			// when
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status_not_changed", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithStatus(username, userID)
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "Provisioning", "")

			r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

			// when
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(failed("", ""))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status_changed_with_error", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithStatus(username, userID)
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "UnableToProvisionNamespace", "error message")

			r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

			// when
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(failed("UnableToProvisionNamespace", "error message"))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status_changed_ready_ok", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithStatus(username, userID)

			r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

			// when
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioned())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})
	})

	t.Run("create nstmplset failed", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
			cl.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create NSTemplateSet")
			}

			// test
			_, err := r.Reconcile(req)

			require.Error(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(failed("UnableToCreateNSTemplateSet", "unable to create NSTemplateSet"))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				DoesNotExist()
		})

		t.Run("provision status failed", func(t *testing.T) {
			r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update status")
			}

			// test
			_, err := r.Reconcile(req)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to update status")
		})

		t.Run("namespace provision status failed", func(t *testing.T) {
			userAcc := newUserAccountWithStatus(username, userID)
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "UnableToProvisionNamespace", "error message")

			r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update status")
			}

			// test
			_, err := r.Reconcile(req)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to update status")
		})

	})

	// Last cycle of reconcile. User, Identity created/updated.
	t.Run("provisioned", func(t *testing.T) {
		// given
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(provisioned())
	})

	t.Run("update when tierName is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.TierName = "advanced"
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("advanced").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when namespace templateRef in NSTemplateSet is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.Namespaces[0].TemplateRef = "basic-dev-09876"
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs("basic-dev-09876", codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when one namespace templateRef is removed", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.Namespaces = userAcc.Spec.NSTemplateSet.Namespaces[1:2]
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when one namespace templateRef is added", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.Namespaces = append(userAcc.Spec.NSTemplateSet.Namespaces,
			toolchainv1alpha1.NSTemplateSetNamespace{TemplateRef: "basic-stage-1234"})
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef, "basic-stage-1234")
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when ClusterResources templateRef in NSTemplateSet is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.ClusterResources.TemplateRef = "basic-clusterresources-007"
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesTemplateRef("basic-clusterresources-007").
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when ClusterResources object is set to nil", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.ClusterResources = nil
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesNil().
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when original ClusterResources object was nil but is defined in UserAccount", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		nsTemplateSet := preexistingNsTmplSet.DeepCopy()
		nsTemplateSet.Spec.ClusterResources = nil
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, nsTemplateSet)

		//when
		_, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("set failed reason when update of NSTemplateSet fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		userAcc.Spec.NSTemplateSet.TierName = "advanced"
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)
		cl.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "NSTemplateSet" {
				return fmt.Errorf("some error")
			}
			return nil
		}

		//when
		_, err := r.Reconcile(req)

		//then
		require.Error(t, err)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).HasConditions(toolchainv1alpha1.Condition{
			Type:    toolchainv1alpha1.ConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  "NSTemplateSetUpdateFailed",
			Message: "some error",
		})
	})

	// Delete useraccount and ensure related resources are also removed
	t.Run("delete useraccount removes subsequent resources", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Terminating", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, identity)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Terminating", "deleting user/identity"))

		// Check that the associated user has been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// Check that the user account finalizer has been removed
		// when reconciling the useraccount with a deletion timestamp
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)
		require.False(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
	})
	// Add finalizer fails
	t.Run("add finalizer fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		// Mock setting finalizer failure
		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return fmt.Errorf("unable to add finalizer for user account %s", userAcc.Name)
		}

		//when
		res, err := r.Reconcile(req)

		//then
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("unable to add finalizer for user account %s", userAcc.Name))
	})
	// Remove finalizer fails
	t.Run("remove finalizer fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// Mock finalizer removal failure
		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			finalizers := []string{toolchainv1alpha1.FinalizerName}
			userAcc := obj.(*toolchainv1alpha1.UserAccount)
			userAcc.Finalizers = finalizers
			return fmt.Errorf("unable to remove finalizer for user account %s", userAcc.Name)
		}

		res, err = r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", "deleting user/identity"))

		res, err = r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", "deleting user/identity"))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("failed to remove finalizer: unable to remove finalizer for user account %s", userAcc.Name))

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", fmt.Sprintf("unable to remove finalizer for user account %s", userAcc.Name)))

		// Check that the associated identity has been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, identity)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check that the associated user has been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check that the user account finalizer has not been removed
		// when reconciling the useraccount with a deletion timestamp
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
	})
	// delete identity fails
	t.Run("delete identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// Mock deleting identity failure
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("unable to delete identity for user account %s", userAcc.Name)
		}

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("failed to delete user/identity: unable to delete identity for user account %s", userAcc.Name))

		// Check that the associated identity has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, identity)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", fmt.Sprintf("unable to delete identity for user account %s", userAcc.Name)))
	})
	// delete user fails
	t.Run("delete user/identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// Mock deleting user failure
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("unable to delete user/identity for user account %s", userAcc.Name)
		}

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("failed to delete user/identity: unable to delete user/identity for user account %s", userAcc.Name))

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", fmt.Sprintf("unable to delete user/identity for user account %s", userAcc.Name)))

		// Check that the associated identity has been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config)}, identity)
		require.NoError(t, err)

		// Check that the associated user has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)
	})
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
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: fakeClient,
			scheme: s,
		}
		condition := toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}

		// when
		err := reconciler.updateStatusConditions(userAcc, condition)

		// then
		require.NoError(t, err)
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: test.MemberOperatorNs, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedAcc.Status.Conditions, condition)
	})

	t.Run("status not updated because not changed", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: fakeClient,
			scheme: s,
		}
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
		}}
		userAcc.Status.Conditions = conditions

		// when
		err := reconciler.updateStatusConditions(userAcc, conditions...)

		// then
		require.NoError(t, err)
		updatedAcc := &toolchainv1alpha1.UserAccount{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: test.MemberOperatorNs, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		// Status is not updated
		test.AssertConditionsMatch(t, updatedAcc.Status.Conditions)
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &ReconcileUserAccount{
			client: fakeClient,
			scheme: s,
		}
		log := logf.Log.WithName("test")

		t.Run("status updated", func(t *testing.T) {
			statusUpdater := func(userAcc *toolchainv1alpha1.UserAccount, message string) error {
				assert.Equal(t, "oopsy woopsy", message)
				return nil
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, userAcc, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create %s", "user bob")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create user bob: oopsy woopsy", err.Error())
		})

		t.Run("status update failed", func(t *testing.T) {
			statusUpdater := func(userAcc *toolchainv1alpha1.UserAccount, message string) error {
				return errors.New("unable to update status")
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, userAcc, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create %s", "user bob")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create user bob: oopsy woopsy", err.Error())
		})
	})
}

func TestDisabledUserAccount(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johndoe"
	userID := uuid.NewV4().String()
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	config, err := configuration.LoadConfig(test.NewFakeClient(t))
	require.NoError(t, err)

	userAcc := newUserAccount(username, userID, false)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name: ToIdentityName(userAcc.Spec.UserID, config),
		UID:  types.UID(username + "identity"),
	}, User: corev1.ObjectReference{
		Name: username,
		UID:  userUID,
	}}
	preexistingUser := &userv1.User{ObjectMeta: metav1.ObjectMeta{
		Name:   username,
		UID:    userUID,
		Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
	}, Identities: []string{ToIdentityName(userAcc.Spec.UserID, config)}}
	preexistingNsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userAcc.Name,
			Namespace: test.MemberOperatorNs,
		},
		Spec: newNSTmplSetSpec(),
		Status: toolchainv1alpha1.NSTemplateSetStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{Type: toolchainv1alpha1.ConditionReady, Status: corev1.ConditionTrue},
			},
		},
	}

	t.Run("disabling useraccount", func(t *testing.T) {
		// given
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		// when
		userAcc.Spec.Disabled = true
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		//then
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabled useraccount", func(t *testing.T) {
		userAcc := newUserAccount(username, userID, true)

		// given
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingNsTmplSet)

		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabled", ""))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabling useraccount without user", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, true)
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingIdentity, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// then
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabling useraccount without identity", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, true)
		r, req, cl := prepareReconcile(t, username, userAcc, preexistingUser, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("deleting disabled useraccount", func(t *testing.T) {

		userAcc := newDisabledUserAccountWithFinalizer(username, userID)

		r, req, cl := prepareReconcile(t, username, userAcc, preexistingNsTmplSet)

		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabled", ""))

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)

		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)
		require.False(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
	})
}

func assertUserNotFound(t *testing.T, r *ReconcileUserAccount, account *toolchainv1alpha1.UserAccount) {
	// Check that the associated user has been deleted
	// since disabled has been set to true
	user := &userv1.User{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: account.Name}, user)
	require.Error(t, err)
	assert.True(t, apierros.IsNotFound(err))
}

func assertIdentityNotFound(t *testing.T, r *ReconcileUserAccount, identityName string) {
	// Check that the associated identity has been deleted
	// since disabled has been set to true
	identity := &userv1.Identity{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	require.Error(t, err)
	assert.True(t, apierros.IsNotFound(err))
}

func assertNSTemplateFound(t *testing.T, r *ReconcileUserAccount, account *toolchainv1alpha1.UserAccount) {
	// Get NSTemplate
	tmplTier := &toolchainv1alpha1.NSTemplateSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: account.Name, Namespace: test.MemberOperatorNs}, tmplTier)
	require.NoError(t, err)
}

func newUserAccount(userName, userID string, disabled bool) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: test.MemberOperatorNs,
			UID:       types.UID(uuid.NewV4().String()),
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID: userID,
			UserAccountSpecBase: toolchainv1alpha1.UserAccountSpecBase{
				NSTemplateSet: newNSTmplSetSpec(),
			},
			Disabled: disabled,
		},
	}
	return userAcc
}

func newUserAccountWithFinalizer(userName, userID string) *toolchainv1alpha1.UserAccount {
	finalizers := []string{toolchainv1alpha1.FinalizerName}
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:       userName,
			Namespace:  test.MemberOperatorNs,
			UID:        types.UID(uuid.NewV4().String()),
			Finalizers: finalizers,
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID:   userID,
			Disabled: false,
		},
	}
	return userAcc
}

func newDisabledUserAccountWithFinalizer(userName, userID string) *toolchainv1alpha1.UserAccount {
	finalizers := []string{toolchainv1alpha1.FinalizerName}
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:       userName,
			Namespace:  test.MemberOperatorNs,
			UID:        types.UID(uuid.NewV4().String()),
			Finalizers: finalizers,
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID:   userID,
			Disabled: true,
		},
	}
	return userAcc
}

func newUserAccountWithStatus(userName, userID string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: test.MemberOperatorNs,
			UID:       types.UID(uuid.NewV4().String()),
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID: userID,
			UserAccountSpecBase: toolchainv1alpha1.UserAccountSpecBase{
				NSTemplateSet: newNSTmplSetSpec(),
			},
		},
		Status: toolchainv1alpha1.UserAccountStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{Type: toolchainv1alpha1.ConditionReady, Status: corev1.ConditionFalse},
			},
		},
	}
	return userAcc
}

const (
	clusterResourcesTemplateRef = "basic-clusterresources-abcde00"
	devTemplateRef              = "basic-dev-abcde11"
	codeTemplateRef             = "basic-code-abcde21"
)

func newNSTmplSetSpec() toolchainv1alpha1.NSTemplateSetSpec {
	return toolchainv1alpha1.NSTemplateSetSpec{
		TierName: "basic",
		ClusterResources: &toolchainv1alpha1.NSTemplateSetClusterResources{
			TemplateRef: clusterResourcesTemplateRef,
		},
		Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
			{TemplateRef: devTemplateRef},
			{TemplateRef: codeTemplateRef},
		},
	}
}

func newNSTmplSetWithStatus(username, reason, meessage string) *toolchainv1alpha1.NSTemplateSet {
	return &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: test.MemberOperatorNs,
		},
		Spec: newNSTmplSetSpec(),
		Status: toolchainv1alpha1.NSTemplateSetStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{
					Type:    toolchainv1alpha1.ConditionReady,
					Status:  corev1.ConditionFalse,
					Reason:  reason,
					Message: meessage,
				},
			},
		},
	}
}

func newReconcileRequest(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: test.MemberOperatorNs,
		},
	}
}

func checkMapping(t *testing.T, user *userv1.User, identity *userv1.Identity) {
	assert.Equal(t, user.Name, identity.User.Name)
	assert.Equal(t, user.UID, identity.User.UID)
	require.Len(t, user.Identities, 1)
	assert.Equal(t, identity.Name, user.Identities[0])
}

func prepareReconcile(t *testing.T, username string, initObjs ...runtime.Object) (*ReconcileUserAccount, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)
	config, err := configuration.LoadConfig(fakeClient)
	require.NoError(t, err)

	r := &ReconcileUserAccount{
		client: fakeClient,
		scheme: s,
		config: config,
	}
	return r, newReconcileRequest(username), fakeClient
}

func updating() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: "Updating",
	}
}

func failed(reason, msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: msg,
	}
}

func provisioned() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: "Provisioned",
	}
}

func provisioning() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: "Provisioning",
	}
}
