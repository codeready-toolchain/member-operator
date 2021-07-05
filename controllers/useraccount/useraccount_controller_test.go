package useraccount

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	memberCfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	"github.com/codeready-toolchain/toolchain-common/pkg/test/useraccount"

	routev1 "github.com/openshift/api/route/v1"
	userv1 "github.com/openshift/api/user/v1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/h2non/gock.v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" //nolint: staticcheck // not deprecated anymore: see https://github.com/kubernetes-sigs/controller-runtime/pull/1101
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testCheURL      = "https://codeready-codeready-workspaces-operator.member-cluster"
	testKeycloakURL = "https://keycloak-codeready-workspaces-operator.member-cluster"
)

func TestReconcile(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	username := "johnsmith"
	userID := uuid.NewV4().String()

	config, err := memberCfg.GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
	require.NoError(t, err)

	// given
	userAcc := newUserAccount(username, userID, false)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
		UID:  types.UID(username + "identity"),
	}, User: corev1.ObjectReference{
		Name: username,
		UID:  userUID,
	}}
	preexistingUser := &userv1.User{ObjectMeta: metav1.ObjectMeta{
		Name:   username,
		UID:    userUID,
		Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
	}, Identities: []string{ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}}
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
		r, req, _, _ := prepareReconcile(t, username)
		//when
		res, err := r.Reconcile(req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check the user is not created
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, &userv1.User{})
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check the identity is not created
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, &userv1.Identity{})
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check the NSTmplSet is not created
		AssertThatNSTemplateSet(t, req.Namespace, userAcc.Name, r.Client).
			DoesNotExist()
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create or update user OK", func(t *testing.T) {
		reconcile := func(r *Reconciler, req reconcile.Request) {
			//when
			res, err := r.Reconcile(req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			updatedAcc := &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
			require.NoError(t, err)
			test.AssertConditionsMatch(t, updatedAcc.Status.Conditions,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionFalse,
					Reason: "Provisioning",
				})

			// Check the created/updated user
			user := &userv1.User{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
			require.NoError(t, err)
			assert.Equal(t, userAcc.Name, user.Name)
			require.Equal(t, userAcc.Name, user.Labels["toolchain.dev.openshift.com/owner"])
			assert.Empty(t, user.OwnerReferences) // User has no explicit owner reference.

			// Check the user identity mapping
			user.UID = preexistingUser.UID // we have to set UID for the obtained user because the fake client doesn't set it
			checkMapping(t, user, preexistingIdentity)

			// Check the identity is not created yet
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, &userv1.Identity{})
			require.Error(t, err)
			assert.True(t, apierros.IsNotFound(err))

			// Check the NSTmplSet is not created yet
			AssertThatNSTemplateSet(t, req.Namespace, userAcc.Name, r.Client).
				DoesNotExist()
		}

		t.Run("create", func(t *testing.T) {
			r, req, _, _ := prepareReconcile(t, username, userAcc)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:   username,
				UID:    userUID,
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
			}}
			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update user failed", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc)
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
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
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
		reconcile := func(r *Reconciler, req reconcile.Request) {
			//when
			res, err := r.Reconcile(req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status is now "provisioning"
			updatedAcc := &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Namespace: req.Namespace, Name: userAcc.Name}, updatedAcc)
			require.NoError(t, err)
			test.AssertConditionsMatch(t, updatedAcc.Status.Conditions,
				toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionFalse,
					Reason: "Provisioning",
				})

			// Check the created/updated identity
			identity := &userv1.Identity{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
			require.NoError(t, err)
			assert.Equal(t, fmt.Sprintf("%s:%s", config.Auth().Idp(), userAcc.Spec.UserID), identity.Name)
			require.Equal(t, userAcc.Name, identity.Labels["toolchain.dev.openshift.com/owner"])
			assert.Empty(t, identity.OwnerReferences) // Identity has no explicit owner reference.

			// Check the user identity mapping
			checkMapping(t, preexistingUser, identity)
		}

		t.Run("create", func(t *testing.T) {
			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			reconcile(r, req)
		})

		t.Run("update", func(t *testing.T) {
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:   ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
				UID:    types.UID(uuid.NewV4().String()),
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name},
			}}

			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update identity failed", func(t *testing.T) {
		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create identity")
			}

			//when
			res, err := r.Reconcile(req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to create identity '%s': unable to create identity", ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(failed("UnableToCreateIdentity", "unable to create identity"))
		})
		t.Run("update", func(t *testing.T) {
			// given
			userAcc := newUserAccountWithFinalizer(username, userID)
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:   ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
				UID:    types.UID(uuid.NewV4().String()),
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name},
			}}
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
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
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

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

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

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

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

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

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
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
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
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

			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)
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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, nsTemplateSet)

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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)
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

		// when the member operator secret exists and has a che admin user configured then che user deletion is enabled
		cfg := memberCfg.NewMemberOperatorConfigWithReset(t,
			testconfig.Che().
				UserDeletionEnabled(true).
				KeycloakRouteName("keycloak").
				Secret().
				Ref("test-secret").
				CheAdminUsernameKey("che.admin.username").
				CheAdminPasswordKey("che.admin.password"))

		mockCallsCounter := new(int)
		defer gock.OffAll()
		gockTokenSuccess(mockCallsCounter)
		gockFindUserTimes(username, 2, mockCallsCounter)
		gockFindUserNoBody(username, 404, mockCallsCounter)
		gockDeleteUser(204, mockCallsCounter)

		memberOperatorSecret := newSecretWithCheAdminCreds()
		userAcc := newUserAccount(username, userID, false)
		r, req, cl, _ := prepareReconcile(t, username, cfg, userAcc, preexistingUser, preexistingIdentity, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Terminating", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
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
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// Check that the user account finalizer has been removed
		// when reconciling the useraccount with a deletion timestamp
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)
		require.False(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
		require.Equal(t, 6, *mockCallsCounter) // Only 1 reconcile will do the delete (4 calls) followed by 2 reconciles with user exists check only
	})
	// Add finalizer fails
	t.Run("add finalizer fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

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
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.Client.Update(context.TODO(), userAcc)
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
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check that the associated user has been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check that the user account finalizer has not been removed
		// when reconciling the useraccount with a deletion timestamp
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
	})
	// delete Che user fails
	t.Run("delete che user fails because find che user request failed", func(t *testing.T) {
		// given

		// when the member operator secret exists and has a che admin user configured then che user deletion is enabled
		cfg := memberCfg.NewMemberOperatorConfigWithReset(t,
			testconfig.Che().
				UserDeletionEnabled(true).
				KeycloakRouteName("keycloak").
				Secret().
				Ref("test-secret").
				CheAdminUsernameKey("che.admin.username").
				CheAdminPasswordKey("che.admin.password"))

		mockCallsCounter := new(int)
		defer gock.OffAll()
		gockTokenSuccess(mockCallsCounter)
		gockFindUserNoBody("johnsmith", 400, mockCallsCounter) // respond with 400 error to simulate find user request failure

		memberOperatorSecret := newSecretWithCheAdminCreds()
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient, _ := prepareReconcile(t, username, cfg, userAcc, preexistingUser, preexistingIdentity, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

		// when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, `failed to delete Che user data: request to find Che user 'johnsmith' failed, Response status: '400 Bad Request' Body: ''`)

		// Check that the associated identity has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		identity := &userv1.Identity{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
		require.NoError(t, err)

		// Check that the associated user has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)
		require.Equal(t, 2, *mockCallsCounter)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", `request to find Che user 'johnsmith' failed, Response status: '400 Bad Request' Body: ''`))
	})
	// delete identity fails
	t.Run("delete identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.Client.Update(context.TODO(), userAcc)
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
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(failed("Terminating", fmt.Sprintf("unable to delete identity for user account %s", userAcc.Name)))
	})
	// delete user fails
	t.Run("delete user/identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		//then
		userAcc = &toolchainv1alpha1.UserAccount{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
		require.NoError(t, err)

		// Check that the finalizer is present
		require.True(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))

		// Set the deletionTimestamp
		userAcc.DeletionTimestamp = &metav1.Time{time.Now()} //nolint: govet
		err = r.Client.Update(context.TODO(), userAcc)
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
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
		require.NoError(t, err)

		// Check that the associated user has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		user := &userv1.User{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)
	})
}

func TestUpdateStatus(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	username := "johnsmith"
	userID := uuid.NewV4().String()
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	t.Run("status updated", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &Reconciler{
			Client: fakeClient,
			Scheme: s,
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
		err = reconciler.Client.Get(context.TODO(), types.NamespacedName{Namespace: test.MemberOperatorNs, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedAcc.Status.Conditions, condition)
	})

	t.Run("status not updated because not changed", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &Reconciler{
			Client: fakeClient,
			Scheme: s,
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
		err = reconciler.Client.Get(context.TODO(), types.NamespacedName{Namespace: test.MemberOperatorNs, Name: userAcc.Name}, updatedAcc)
		require.NoError(t, err)
		// Status is not updated
		test.AssertConditionsMatch(t, updatedAcc.Status.Conditions)
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		fakeClient := fake.NewFakeClient(userAcc)
		reconciler := &Reconciler{
			Client: fakeClient,
			Scheme: s,
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
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	username := "johndoe"
	userID := uuid.NewV4().String()
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	config, err := memberCfg.GetConfig(test.NewFakeClient(t), test.MemberOperatorNs)
	require.NoError(t, err)

	userAcc := newUserAccount(username, userID, false)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
		Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
		UID:  types.UID(username + "identity"),
	}, User: corev1.ObjectReference{
		Name: username,
		UID:  userUID,
	}}
	preexistingUser := &userv1.User{ObjectMeta: metav1.ObjectMeta{
		Name:   username,
		UID:    userUID,
		Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
	}, Identities: []string{ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}}
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
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		// when
		userAcc.Spec.Disabled = true
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		//then
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		res, err = r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabled useraccount", func(t *testing.T) {
		userAcc := newUserAccount(username, userID, true)

		// given
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingNsTmplSet)

		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabled", ""))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabling useraccount without user", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, true)
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingIdentity, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// then
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("disabling useraccount without identity", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, true)
		r, req, cl, config := prepareReconcile(t, username, userAcc, preexistingUser, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(failed("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		assertNSTemplateFound(t, r, userAcc)
	})

	t.Run("deleting user for disabled useraccount", func(t *testing.T) {
		// given
		userAcc := newDisabledUserAccountWithFinalizer(username, userID)
		userAcc.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingNsTmplSet, preexistingUser, preexistingIdentity)

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()))

		t.Run("deleting identity for disabled useraccount", func(t *testing.T) {
			// when
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)

			// Check that the associated user has been deleted
			// since disabled has been set to true
			assertUserNotFound(t, r, userAcc)

			// Check NSTemplate
			assertNSTemplateFound(t, r, userAcc)

			t.Run("removing finalizer for disabled useraccount", func(t *testing.T) {
				// when
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				userAcc = &toolchainv1alpha1.UserAccount{}
				err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
				require.NoError(t, err)
				require.False(t, util.HasFinalizer(userAcc, toolchainv1alpha1.FinalizerName))
			})
		})
	})
}

func TestLookupAndDeleteCheUser(t *testing.T) {
	// given
	username := "sugar"
	userID := uuid.NewV4().String()

	t.Run("che user deletion is not enabled", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, false)
		r, _, _, config := prepareReconcile(t, username, userAcc, cheRoute(true), keycloackRoute(true))

		// when
		err := r.lookupAndDeleteCheUser(config, userAcc)

		// then
		require.NoError(t, err)
	})

	t.Run("che user deletion is enabled", func(t *testing.T) {
		memberOperatorSecret := newSecretWithCheAdminCreds()

		cfg := memberCfg.NewMemberOperatorConfigWithReset(t,
			testconfig.Che().
				UserDeletionEnabled(true).
				KeycloakRouteName("keycloak").
				Secret().
				Ref("test-secret").
				CheAdminUsernameKey("che.admin.username").
				CheAdminPasswordKey("che.admin.password"))

		t.Run("get token error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenFail(mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, cfg, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			//when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.EqualError(t, err, `request to find Che user 'sugar' failed: unable to obtain access token for che, Response status: '400 Bad Request'. Response body: ''`)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)
			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 1, *mockCallsCounter) // 1. get token
		})

		t.Run("user not found", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 404, mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.NoError(t, err)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)
			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 2, *mockCallsCounter) // 1. get token 2. user exists check
		})

		t.Run("find user error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 400, mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.EqualError(t, err, `request to find Che user 'sugar' failed, Response status: '400 Bad Request' Body: ''`)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)
			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 2, *mockCallsCounter) // 1. get token 2. user exists check
		})

		t.Run("find user ID parse error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 200, mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.EqualError(t, err, `unable to get Che user ID for user 'sugar': error unmarshalling Che user json  : unexpected end of JSON input`)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)
			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 3, *mockCallsCounter) // 1. get token 2. user exists check 3. get user ID
		})

		t.Run("delete error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserTimes(username, 2, mockCallsCounter)
			gockDeleteUser(400, mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.EqualError(t, err, `this error is expected if deletion is still in progress: unable to delete Che user with ID 'abc1234', Response status: '400 Bad Request' Body: ''`)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)
			require.Equal(t, 4, *mockCallsCounter) // 1. get token 2. check user exists 3. get user ID 4. delete user
		})

		t.Run("successful lookup and delete", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserTimes(username, 2, mockCallsCounter)
			gockDeleteUser(204, mockCallsCounter)
			userAcc := newUserAccount(username, userID, false)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(config, userAcc)

			// then
			require.NoError(t, err)
			userAcc = &toolchainv1alpha1.UserAccount{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: test.MemberOperatorNs}, userAcc)
			require.NoError(t, err)

			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 4, *mockCallsCounter) // 1. get token 2. check user exists 3. get user ID 4. delete user
		})

	})

}

func assertUserNotFound(t *testing.T, r *Reconciler, account *toolchainv1alpha1.UserAccount) {
	// Check that the associated user has been deleted
	// since disabled has been set to true
	user := &userv1.User{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: account.Name}, user)
	require.Error(t, err)
	assert.True(t, apierros.IsNotFound(err))
}

func assertIdentityNotFound(t *testing.T, r *Reconciler, identityName string) {
	// Check that the associated identity has been deleted
	// since disabled has been set to true
	identity := &userv1.Identity{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	require.Error(t, err)
	assert.True(t, apierros.IsNotFound(err))
}

func assertNSTemplateFound(t *testing.T, r *Reconciler, account *toolchainv1alpha1.UserAccount) {
	// Get NSTemplate
	tmplTier := &toolchainv1alpha1.NSTemplateSet{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: account.Name, Namespace: test.MemberOperatorNs}, tmplTier)
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

func prepareReconcile(t *testing.T, username string, initObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient, memberCfg.Configuration) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)
	config, err := memberCfg.GetConfig(fakeClient, test.MemberOperatorNs)
	require.NoError(t, err)

	tc := che.NewTokenCache(http.DefaultClient)
	cheClient := che.NewCheClient(config, http.DefaultClient, fakeClient, tc)

	config, err = memberCfg.GetConfig(fakeClient, test.MemberOperatorNs)
	require.NoError(t, err)

	r := &Reconciler{
		Client:    fakeClient,
		Scheme:    s,
		CheClient: cheClient,
		Log:       ctrl.Log.WithName("controllers").WithName("UserAccount"),
	}
	return r, newReconcileRequest(username), fakeClient, config
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

func cheRoute(tls bool) *routev1.Route { //nolint: unparam
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codeready",
			Namespace: "codeready-workspaces-operator",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("codeready-codeready-workspaces-operator.%s", test.MemberClusterName),
			Path: "",
		},
	}
	if tls {
		r.Spec.TLS = &routev1.TLSConfig{
			Termination: "edge",
		}
	}
	return r
}

func keycloackRoute(tls bool) *routev1.Route { //nolint: unparam
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "keycloak",
			Namespace: "codeready-workspaces-operator",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("keycloak-codeready-workspaces-operator.%s", test.MemberClusterName),
			Path: "",
		},
	}
	if tls {
		r.Spec.TLS = &routev1.TLSConfig{
			Termination: "edge",
		}
	}
	return r
}

func newSecretWithCheAdminCreds() *v1.Secret {
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: test.MemberOperatorNs,
		},
		Data: map[string][]byte{
			"che.admin.username": []byte("test-che-user"),
			"che.admin.password": []byte("test-che-password"),
		},
	}
}

func gockTokenSuccess(calls *int) {
	gock.New(testKeycloakURL).
		Post("auth/realms/codeready/protocol/openid-connect/token").
		SetMatcher(SpyOnGockCalls(calls)).
		MatchHeader("Content-Type", "application/x-www-form-urlencoded").
		Persist().
		Reply(200).
		BodyString(`{
				"access_token":"abc.123.xyz",
				"expires_in":300,
				"refresh_expires_in":1800,
				"refresh_token":"111.222.333",
				"token_type":"bearer",
				"not-before-policy":0,
				"session_state":"a2fa1448-687a-414f-af40-3b6b3f5a873a",
				"scope":"profile email"
				}`)
}

func gockTokenFail(calls *int) {
	gock.New(testKeycloakURL).
		Post("auth/realms/codeready/protocol/openid-connect/token").
		SetMatcher(SpyOnGockCalls(calls)).
		MatchHeader("Content-Type", "application/x-www-form-urlencoded").
		Persist().
		Reply(400)
}

func gockFindUserTimes(name string, times int, calls *int) {
	gock.New(testCheURL).
		Get("api/user/find").
		SetMatcher(SpyOnGockCalls(calls)).
		MatchHeader("Authorization", "Bearer abc.123.xyz").
		Times(times).
		Reply(200).
		BodyString(fmt.Sprintf(`{"name":"%s","id":"abc1234"}`, name))
}

func gockFindUserNoBody(name string, code int, calls *int) { //nolint: unparam
	gock.New(testCheURL).
		Get("api/user/find").
		SetMatcher(SpyOnGockCalls(calls)).
		MatchHeader("Authorization", "Bearer abc.123.xyz").
		Persist().
		Reply(code)
}

func gockDeleteUser(code int, calls *int) {
	gock.New(testCheURL).
		Delete("api/user").
		SetMatcher(SpyOnGockCalls(calls)).
		MatchHeader("Authorization", "Bearer abc.123.xyz").
		Persist().
		Reply(code)
}

func SpyOnGockCalls(counter *int) gock.Matcher {
	matcher := gock.NewBasicMatcher()
	matcher.Add(func(_ *http.Request, _ *gock.Request) (bool, error) {
		*counter++
		return true, nil
	})
	return matcher
}
