package useraccount

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	identity2 "github.com/codeready-toolchain/toolchain-common/pkg/identity"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	membercfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
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
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
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
	os.Setenv("WATCH_NAMESPACE", test.MemberOperatorNs)

	username := "johnsmith"
	userID := uuid.NewV4().String()

	config, err := membercfg.GetConfiguration(test.NewFakeClient(t))
	require.NoError(t, err)

	// given
	userAcc := newUserAccount(username, userID)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
			UID:  types.UID(userAcc.Name + "identity"),
			Labels: map[string]string{
				"toolchain.dev.openshift.com/owner": username,
				toolchainv1alpha1.ProviderLabelKey:  toolchainv1alpha1.ProviderLabelValue,
			},
		},
		User: corev1.ObjectReference{
			Name: username,
			UID:  userUID,
		},
	}
	preexistingUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: userAcc.Name,
			UID:  userUID,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/owner": username,
				toolchainv1alpha1.ProviderLabelKey:  toolchainv1alpha1.ProviderLabelValue,
			},
		},
		Identities: []string{
			ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
		},
	}
	preexistingNsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userAcc.Name,
			Namespace: test.MemberOperatorNs,
		},
		Spec: newNSTmplSetSpec(),
		Status: toolchainv1alpha1.NSTemplateSetStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	t.Run("deleted account ignored", func(t *testing.T) {
		// given
		// No user account exists
		r, req, _, _ := prepareReconcile(t, username)
		//when
		res, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// Check the user is not created
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, &userv1.User{})
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		// Check the identity is not created
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		// Check the NSTmplSet is not created
		AssertThatNSTemplateSet(t, req.Namespace, userAcc.Name, r.Client).
			DoesNotExist()
	})

	// First cycle of reconcile. Freshly created UserAccount.
	t.Run("create or update user OK", func(t *testing.T) {
		reconcile := func(r *Reconciler, req reconcile.Request) {
			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, username, r.Client).
				HasConditions(provisioning())

			// Check the created/updated user
			user := assertUser(t, r, userAcc)
			user.UID = preexistingUser.UID // we have to set UID for the obtained user because the fake client doesn't set it
			checkMapping(t, user, preexistingIdentity)

			// Check the identity is not created yet
			assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

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
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": username, toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue},
			}}
			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update user failed", func(t *testing.T) {

		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc)
			fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create user")
			}

			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to create user '%s': unable to create user", username))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(notReady("UnableToCreateUser", "unable to create user"))
		})

		t.Run("update", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withFinalizer())
			preexistingUserWithNoMapping := &userv1.User{ObjectMeta: metav1.ObjectMeta{
				Name:   username,
				UID:    userUID,
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": username, toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue},
			}}
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUserWithNoMapping)
			fakeClient.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update user")
			}

			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to update user '%s': unable to update user", username))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(notReady("UnableToCreateMapping", "unable to update user"))
		})
	})

	// Second cycle of reconcile. User already created.
	t.Run("create or update identity OK", func(t *testing.T) {
		reconcile := func(r *Reconciler, req reconcile.Request) {
			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status is now "provisioning"
			useraccount.AssertThatUserAccount(t, username, r.Client).
				HasConditions(provisioning())

			// Check the created/updated identity
			identity := assertIdentity(t, r, userAcc, config.Auth().Idp())

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
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name, toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue},
			}}

			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			reconcile(r, req)
		})
	})

	t.Run("create or update identity failed", func(t *testing.T) {

		t.Run("create", func(t *testing.T) {
			// given
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create identity")
			}

			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to create identity '%s': unable to create identity", ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(notReady("UnableToCreateIdentity", "unable to create identity"))
		})

		t.Run("update", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withFinalizer())
			preexistingIdentityWithNoMapping := &userv1.Identity{ObjectMeta: metav1.ObjectMeta{
				Name:   ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
				UID:    types.UID(uuid.NewV4().String()),
				Labels: map[string]string{"toolchain.dev.openshift.com/owner": userAcc.Name},
			}}
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentityWithNoMapping)
			fakeClient.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update identity")
			}

			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.EqualError(t, err, fmt.Sprintf("failed to update identity '%s': unable to update identity", preexistingIdentityWithNoMapping.Name))
			assert.Equal(t, reconcile.Result{}, res)

			// Check that the user account status has been updated
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(notReady("UnableToCreateMapping", "unable to update identity"))
		})
	})

	t.Run("create nstmplset OK", func(t *testing.T) {

		t.Run("create", func(t *testing.T) {
			// given
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status not changed when NSTemplateSet is being provisioned", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withNotReadyCondition("ShouldStay"))
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "Provisioning", "")

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(notReady("ShouldStay", ""))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status changed when NSTemplateSet is being updated and useraccount is Provisioned", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withReadyCondition("Provisioned"))
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "Updating", "")

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(notReady("Updating", ""))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status_changed_with_error", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withNotReadyCondition("Provisioning"))
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "UnableToProvisionNamespace", "error message")

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(notReady("UnableToProvisionNamespace", "error message"))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status changed from provisioning to ready", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withNotReadyCondition("Provisioning"))

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioned())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status stays the same as is updating and set recently", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withNotReadyCondition("Updating"))

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(updating())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("status changed from updating to ready as was set more than 1 sec ago", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID, withNotReadyCondition("Updating"))
			userAcc.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Second))

			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioned())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("basic").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})
	})

	t.Run("create nstmplset failed", func(t *testing.T) {

		t.Run("create", func(t *testing.T) {
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
			cl.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				return errors.New("unable to create NSTemplateSet")
			}

			// test
			_, err := r.Reconcile(context.TODO(), req)

			require.Error(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(notReady("UnableToCreateNSTemplateSet", "unable to create NSTemplateSet"))
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				DoesNotExist()
		})

		t.Run("provision status failed", func(t *testing.T) {
			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update status")
			}

			// test
			_, err := r.Reconcile(context.TODO(), req)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to update status")
		})

		t.Run("namespace provision status failed", func(t *testing.T) {
			userAcc := newUserAccount(username, userID, withNotReadyCondition("Provisioning"))
			preexistingNsTmplSetWithNS := newNSTmplSetWithStatus(userAcc.Name, "UnableToProvisionNamespace", "error message")

			r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSetWithNS)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return errors.New("unable to update status")
			}

			// test
			_, err := r.Reconcile(context.TODO(), req)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to update status")
		})

		t.Run("no nstemplateset to create", func(t *testing.T) {
			userAcc := newUserAccount(username, userID, withoutNSTemplateSet())
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioned())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).DoesNotExist()

		})

		t.Run("no nstemplateset to update", func(t *testing.T) {
			userAcc := newUserAccount(username, userID, withoutNSTemplateSet(), withFinalizer())
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)
			cl.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				return fmt.Errorf("schouldn't be called")
			}

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioned())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				Exists(). // existed before
				HasNoOwnerReferences().
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef).
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef)
		})

	})

	// Last cycle of reconcile. User, Identity created/updated.
	t.Run("provisioned", func(t *testing.T) {
		// given
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		res, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(provisioned())
	})

	t.Run("update when tierName is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.TierName = "advanced"
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("advanced").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when namespace templateRef in NSTemplateSet is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.Namespaces[0].TemplateRef = "basic-dev-09876"
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs("basic-dev-09876", codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when one namespace templateRef is removed", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.Namespaces = userAcc.Spec.NSTemplateSet.Namespaces[1:2]
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when one namespace templateRef is added", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.Namespaces = append(userAcc.Spec.NSTemplateSet.Namespaces,
			toolchainv1alpha1.NSTemplateSetNamespace{TemplateRef: "basic-stage-1234"})
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef, "basic-stage-1234")
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when ClusterResources templateRef in NSTemplateSet is different", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.ClusterResources.TemplateRef = "basic-clusterresources-007"
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesTemplateRef("basic-clusterresources-007").
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when ClusterResources object is set to nil", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.ClusterResources = nil
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesNil().
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("update when original ClusterResources object was nil but is defined in UserAccount", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		nsTemplateSet := preexistingNsTmplSet.DeepCopy()
		nsTemplateSet.Spec.ClusterResources = nil
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, nsTemplateSet)

		//when
		_, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, req.Namespace, username, cl).
			HasNoOwnerReferences().
			HasTierName("basic").
			HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
			HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		useraccount.AssertThatUserAccount(t, userAcc.Name, cl).
			HasConditions(updating())
	})

	t.Run("set failed reason when update of NSTemplateSet fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.NSTemplateSet.TierName = "advanced"
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet)
		cl.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "NSTemplateSet" {
				return fmt.Errorf("some error")
			}
			return nil
		}

		//when
		_, err := r.Reconcile(context.TODO(), req)

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

		t.Run("with NSTemlateSet", func(t *testing.T) {
			// given

			// when the member operator secret exists and has a che admin user configured then che user deletion is enabled
			cfg := commonconfig.NewMemberOperatorConfigWithReset(t,
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
			userAcc := newUserAccount(username, userID)
			util.AddFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
			r, req, cl, _ := prepareReconcile(t, username, cfg, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			t.Run("first reconcile deletes identity", func(t *testing.T) {
				// given
				// Set the deletionTimestamp
				now := metav1.NewTime(time.Now())
				userAcc.DeletionTimestamp = &now
				err = r.Client.Update(context.TODO(), userAcc)
				require.NoError(t, err)

				// when
				res, err := r.Reconcile(context.TODO(), req)

				// then
				assert.Equal(t, reconcile.Result{}, res)
				require.NoError(t, err)

				useraccount.AssertThatUserAccount(t, req.Name, cl).
					HasConditions(notReady("Terminating", "deleting user/identity"))

				// Check that the associated identity has been deleted
				// when reconciling the useraccount with a deletion timestamp
				assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())
				useraccount.AssertThatUserAccount(t, userAcc.Name, cl).HasConditions(terminating("deleting user/identity"))

				t.Run("second reconcile deletes user", func(t *testing.T) {
					// when
					res, err = r.Reconcile(context.TODO(), req)

					// then
					assert.Equal(t, reconcile.Result{}, res)
					require.NoError(t, err)

					useraccount.AssertThatUserAccount(t, req.Name, cl).
						HasConditions(notReady("Terminating", "deleting user/identity"))

					// Check that the associated user has been deleted
					// when reconciling the useraccount with a deletion timestamp
					assertUserNotFound(t, r, userAcc)
					useraccount.AssertThatUserAccount(t, userAcc.Name, cl).HasConditions(terminating("deleting user/identity"))

					t.Run("third reconcile deletes NSTemplateSet", func(t *testing.T) {
						// when
						res, err = r.Reconcile(context.TODO(), req)

						// then
						assert.Equal(t, reconcile.Result{}, res)
						require.NoError(t, err)

						// Check that the user account finalizer has been removed
						// when reconciling the useraccount with a deletion timestamp
						useraccount.AssertThatUserAccount(t, username, r.Client).
							HasConditions(terminating("deleting NSTemplateSet"))

						t.Run("fourth reconcile removes finalizer", func(t *testing.T) {
							// when
							res, err = r.Reconcile(context.TODO(), req)

							// then
							assert.Equal(t, reconcile.Result{}, res)
							require.NoError(t, err)

							// Check that the user account finalizer has been removed
							// when reconciling the useraccount with a deletion timestamp
							useraccount.AssertThatUserAccount(t, username, r.Client).
								HasNoFinalizer()
							require.Equal(t, 7, *mockCallsCounter)
						})
					})
				})
			})
		})

		t.Run("without NSTemlateSet", func(t *testing.T) {
			// given

			// when the member operator secret exists and has a che admin user configured then che user deletion is enabled
			cfg := commonconfig.NewMemberOperatorConfigWithReset(t,
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
			userAcc := newUserAccount(username, userID, withoutNSTemplateSet())
			util.AddFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
			r, req, cl, _ := prepareReconcile(t, username, cfg, userAcc, preexistingUser, preexistingIdentity, preexistingNsTmplSet, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			t.Run("first reconcile deletes identity", func(t *testing.T) {
				// given
				// Set the deletionTimestamp
				now := metav1.NewTime(time.Now())
				userAcc.DeletionTimestamp = &now
				err = r.Client.Update(context.TODO(), userAcc)
				require.NoError(t, err)

				// when
				res, err := r.Reconcile(context.TODO(), req)

				// then
				assert.Equal(t, reconcile.Result{}, res)
				require.NoError(t, err)

				useraccount.AssertThatUserAccount(t, req.Name, cl).
					HasConditions(notReady("Terminating", "deleting user/identity"))

				// Check that the associated identity has been deleted
				// when reconciling the useraccount with a deletion timestamp
				assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())
				useraccount.AssertThatUserAccount(t, userAcc.Name, cl).HasConditions(terminating("deleting user/identity"))

				t.Run("second reconcile deletes user", func(t *testing.T) {
					// when
					res, err = r.Reconcile(context.TODO(), req)

					// then
					assert.Equal(t, reconcile.Result{}, res)
					require.NoError(t, err)

					useraccount.AssertThatUserAccount(t, req.Name, cl).
						HasConditions(notReady("Terminating", "deleting user/identity"))

					// Check that the associated user has been deleted
					// when reconciling the useraccount with a deletion timestamp
					assertUserNotFound(t, r, userAcc)
					useraccount.AssertThatUserAccount(t, userAcc.Name, cl).HasConditions(terminating("deleting user/identity"))

					t.Run("third reconcile removes finalizer", func(t *testing.T) {
						// when
						res, err = r.Reconcile(context.TODO(), req)

						// then
						assert.Equal(t, reconcile.Result{}, res)
						require.NoError(t, err)

						// Check that the user account finalizer has been removed
						// when reconciling the useraccount with a deletion timestamp
						useraccount.AssertThatUserAccount(t, username, r.Client).
							HasNoFinalizer()
						require.Equal(t, 6, *mockCallsCounter)
					})
				})
			})
		})
	})

	t.Run("for AppStudio tier", func(t *testing.T) {
		// given
		appStudioAccount := userAcc.DeepCopy()
		appStudioAccount.Spec.NSTemplateSet.TierName = "appstudio"
		appStudioAccount.Labels[toolchainv1alpha1.TierLabelKey] = "appstudio"

		t.Run("tiername is appstudio - no user nor identity", func(t *testing.T) {
			// given
			r, req, cl, _ := prepareReconcile(t, username, appStudioAccount)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)

			assertUserNotFound(t, r, userAcc)
			assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("appstudio").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})

		t.Run("user & identity are there - it should remove identity as it has the owner label set", func(t *testing.T) {
			// given
			r, req, cl, _ := prepareReconcile(t, username, appStudioAccount, preexistingUser, preexistingIdentity)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)

			assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())
			assertUser(t, r, userAcc)

			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				DoesNotExist()
		})

		t.Run("user is there - it should remove the user and not identity as it doesn't have owner label set", func(t *testing.T) {
			// given
			withoutLabel := preexistingIdentity.DeepCopy()
			withoutLabel.Labels = nil
			r, req, cl, _ := prepareReconcile(t, username, appStudioAccount, withoutLabel, preexistingUser)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)

			identity := &userv1.Identity{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
			require.NoError(t, err)
			assertUserNotFound(t, r, userAcc)

			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				DoesNotExist()
		})

		t.Run("user & identity are there, but none of them should be removed - they don't have owner label set", func(t *testing.T) {
			// given
			identityWithoutLabel := preexistingIdentity.DeepCopy()
			identityWithoutLabel.Labels = nil
			userWithoutLabel := preexistingUser.DeepCopy()
			userWithoutLabel.Labels = nil
			r, req, cl, _ := prepareReconcile(t, username, appStudioAccount, identityWithoutLabel, userWithoutLabel)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)

			identity := &userv1.Identity{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
			require.NoError(t, err)
			user := &userv1.User{}
			err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
			require.NoError(t, err)

			useraccount.AssertThatUserAccount(t, req.Name, cl).
				HasConditions(provisioning())
			AssertThatNSTemplateSet(t, req.Namespace, req.Name, cl).
				HasNoOwnerReferences().
				HasTierName("appstudio").
				HasClusterResourcesTemplateRef(clusterResourcesTemplateRef).
				HasNamespaceTemplateRefs(devTemplateRef, codeTemplateRef)
		})
	})

	t.Run("useraccount is being deleted and delete calls returns not found - then it should just remove finalizer", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		util.AddFinalizer(userAcc, toolchainv1alpha1.FinalizerName)
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
		cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return apierros.NewNotFound(toolchainv1alpha1.GroupVersion.WithResource("UserAccount").GroupResource(), userAcc.Name)
		}
		now := metav1.NewTime(time.Now())
		userAcc.DeletionTimestamp = &now
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// when
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		useraccount.AssertThatUserAccount(t, username, r.Client).
			HasNoFinalizer().
			HasNoConditions()

		t.Run("when useraccount is being deleted and doesn't have finalizer, then status should stay the same", func(t *testing.T) {
			// given
			userAcc := newUserAccount(username, userID)
			r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return apierros.NewNotFound(toolchainv1alpha1.GroupVersion.WithResource("UserAccount").GroupResource(), userAcc.Name)
			}
			now := metav1.NewTime(time.Now())
			userAcc.DeletionTimestamp = &now
			err = r.Client.Update(context.TODO(), userAcc)
			require.NoError(t, err)

			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, username, r.Client).
				HasNoFinalizer().
				HasNoConditions()
		})
	})

	// Add finalizer fails
	t.Run("add finalizer fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		// Mock setting finalizer failure
		fakeClient.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			return fmt.Errorf("unable to add finalizer for user account %s", userAcc.Name)
		}

		//when
		res, err := r.Reconcile(context.TODO(), req)

		//then
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("unable to add finalizer for user account %s", userAcc.Name))
	})

	// Remove finalizer fails
	t.Run("remove finalizer fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// then
		// Check that the finalizer is present
		userAcc = useraccount.AssertThatUserAccount(t, username, r.Client).
			HasFinalizer(toolchainv1alpha1.FinalizerName).
			Get()

		// Set the deletionTimestamp
		now := metav1.NewTime(time.Now())
		userAcc.DeletionTimestamp = &now
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// trigger the deletion of the `User` resource
		t.Run("first reconcile with Deletion timestamp: deleting the User resource", func(t *testing.T) {
			res, err = r.Reconcile(context.TODO(), req)
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
				HasConditions(notReady("Terminating", "deleting user/identity"))

			// trigger the deletion of the `Identity` resource
			t.Run("second reconcile with Deletion timestamp: deleting the Identity resource", func(t *testing.T) {
				res, err = r.Reconcile(context.TODO(), req)
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
					HasConditions(notReady("Terminating", "deleting user/identity"))

				// trigger the deletion of the `NSTemplateSet` resource
				t.Run("Reconcile #3 with Deletion timestamp: deleting the NSTemplateSet", func(t *testing.T) {
					res, err = r.Reconcile(context.TODO(), req)
					require.NoError(t, err)
					assert.Equal(t, reconcile.Result{}, res)
					useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
						HasConditions(notReady("Terminating", "deleting NSTemplateSet"))

					t.Run("fourth reconcile with Deletion timestamp: attempt to delete the UserAccount", func(t *testing.T) {
						// Mock finalizer removal failure
						fakeClient.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
							if userAcc, ok := obj.(*toolchainv1alpha1.UserAccount); ok {
								userAcc.Finalizers = []string{toolchainv1alpha1.FinalizerName} // restore finalizers
								return fmt.Errorf("unable to remove finalizer for user account %s", userAcc.Name)
							}
							return fakeClient.Client.Update(ctx, obj, opts...)
						}
						res, err = r.Reconcile(context.TODO(), req)
						assert.Equal(t, reconcile.Result{}, res)
						require.EqualError(t, err, fmt.Sprintf("failed to remove finalizer: unable to remove finalizer for user account %s", userAcc.Name))

						useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
							HasConditions(notReady("Terminating", fmt.Sprintf("unable to remove finalizer for user account %s", userAcc.Name)))

						// Check that the associated identity has been deleted
						// when reconciling the useraccount with a deletion timestamp
						assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

						// Check that the associated user has been deleted
						// when reconciling the useraccount with a deletion timestamp
						assertUserNotFound(t, r, userAcc)

						// Check that the user account finalizer has not been removed
						// when reconciling the useraccount with a deletion timestamp
						useraccount.AssertThatUserAccount(t, username, r.Client).HasFinalizer(toolchainv1alpha1.FinalizerName)
					})
				})
			})
		})
	})

	// delete Che user fails
	t.Run("delete che user fails because find che user request failed", func(t *testing.T) {
		// given

		// when the member operator secret exists and has a che admin user configured then che user deletion is enabled
		cfg := commonconfig.NewMemberOperatorConfigWithReset(t,
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
		userAcc := newUserAccount(username, userID)
		r, req, fakeClient, _ := prepareReconcile(t, username, cfg, userAcc, preexistingUser, preexistingIdentity, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

		// when
		res, err := r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// then
		userAcc = useraccount.AssertThatUserAccount(t, username, r.Client).
			HasFinalizer(toolchainv1alpha1.FinalizerName).
			Get()

		// Set the deletionTimestamp
		now := metav1.NewTime(time.Now())
		userAcc.DeletionTimestamp = &now
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		res, err = r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, `failed to delete Che user data: request to find Che user 'johnsmith' failed, Response status: '400 Bad Request' Body: ''`)

		// Check that the associated identity has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		assertIdentity(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		assertUser(t, r, userAcc)
		require.Equal(t, 2, *mockCallsCounter)

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(notReady("Terminating", `request to find Che user 'johnsmith' failed, Response status: '400 Bad Request' Body: ''`))
	})

	// delete identity fails
	t.Run("delete identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// then
		userAcc = useraccount.AssertThatUserAccount(t, username, r.Client).
			HasFinalizer(toolchainv1alpha1.FinalizerName).
			Get()

		// Set the deletionTimestamp
		now := metav1.NewTime(time.Now())
		userAcc.DeletionTimestamp = &now
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// Mock deleting identity failure
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("unable to delete identity for user account %s", userAcc.Name)
		}

		res, err = r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("failed to delete user/identity: unable to delete identity for user account %s", userAcc.Name))

		// Check that the associated identity has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		assertIdentity(t, r, userAcc, config.Auth().Idp())

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(notReady("Terminating", fmt.Sprintf("unable to delete identity for user account %s", userAcc.Name)))
	})

	// delete user fails
	t.Run("delete user/identity fails", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		r, req, fakeClient, _ := prepareReconcile(t, username, userAcc, preexistingUser, preexistingIdentity)

		//when
		res, err := r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// then
		userAcc = useraccount.AssertThatUserAccount(t, username, r.Client).
			HasFinalizer(toolchainv1alpha1.FinalizerName).
			Get()

		// Set the deletionTimestamp
		now := metav1.NewTime(time.Now())
		userAcc.DeletionTimestamp = &now
		err = r.Client.Update(context.TODO(), userAcc)
		require.NoError(t, err)

		// Mock deleting user failure
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("unable to delete user/identity for user account %s", userAcc.Name)
		}

		res, err = r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.EqualError(t, err, fmt.Sprintf("failed to delete user/identity: unable to delete user/identity for user account %s", userAcc.Name))

		useraccount.AssertThatUserAccount(t, req.Name, fakeClient).
			HasConditions(notReady("Terminating", fmt.Sprintf("unable to delete user/identity for user account %s", userAcc.Name)))

		// Check that the associated identity has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		assertIdentity(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has not been deleted
		// when reconciling the useraccount with a deletion timestamp
		assertUser(t, r, userAcc)
	})

	// Test UserAccount with OriginalSub property set
	// TODO remove this test after migration is complete
	t.Run("create or update identities from OriginalSub OK", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.OriginalSub = fmt.Sprintf("original-sub:%s", userID)

		t.Run("create user identity mapping", func(t *testing.T) {
			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			updatedUser := assertUser(t, r, userAcc)

			t.Run("create first identity", func(t *testing.T) {
				r, req, _, _ := prepareReconcile(t, username, userAcc, updatedUser)
				//when
				res, err := r.Reconcile(context.TODO(), req)

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
				identity1 := assertIdentity(t, r, userAcc, config.Auth().Idp())

				t.Run("create second identity", func(t *testing.T) {

					r, req, _, _ := prepareReconcile(t, username, userAcc, updatedUser, identity1)
					//when
					res, err := r.Reconcile(context.TODO(), req)
					//then
					require.NoError(t, err)
					assert.Equal(t, reconcile.Result{}, res)

					// Check the second created/updated identity
					identity2 := &userv1.Identity{}
					err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(
						fmt.Sprintf("b64:%s", base64.RawStdEncoding.EncodeToString([]byte(userAcc.Spec.OriginalSub))),
						config.Auth().Idp())}, identity2)
					require.NoError(t, err)
					assert.Equal(t, fmt.Sprintf("%s:b64:%s", config.Auth().Idp(), base64.RawStdEncoding.EncodeToString([]byte(userAcc.Spec.OriginalSub))), identity2.Name)
					require.Equal(t, userAcc.Name, identity2.Labels["toolchain.dev.openshift.com/owner"])
					assert.Empty(t, identity2.OwnerReferences) // Identity has no explicit owner reference.

					t.Run("reconcile once more to ensure the users", func(t *testing.T) {
						r, req, _, _ := prepareReconcile(t, username, userAcc, updatedUser, identity1, identity2)
						//when
						res, err := r.Reconcile(context.TODO(), req)
						//then
						require.NoError(t, err)
						assert.Equal(t, reconcile.Result{}, res)

						// Check the user identity mapping
						checkMapping(t, updatedUser, identity1, identity2)
					})
				})
			})
		})
	})

	// Test UserAccount with UserID exceeding length limit
	t.Run("create or update identities from excessive length UserID encoded OK", func(t *testing.T) {
		tooLongUserID := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
		userAcc := newUserAccount(username, tooLongUserID)
		r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser)
		//when
		res, err := r.Reconcile(context.TODO(), req)

		//then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)

		// User is expected to be created in first reconcile
		updatedUser := assertUser(t, r, userAcc)
		// Ensure that the user ID has been encoded
		require.Equal(t, "rhd:b64:QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ejAxMjM0"+
			"NTY3ODlBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWmFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6MDEyMzQ1Njc4OUFCQ0RFRkdISUpLTE"+
			"1OT1BRUlNUVVZXWFlaYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU2Nzg5QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNk"+
			"ZWZnaGlqa2xtbm9wcXJzdHV2d3h5ejAxMjM0NTY3ODlBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWmFiY2RlZmdoaWprbG1ub3BxcnN0dX"+
			"Z3eHl6MDEyMzQ1Njc4OQ", updatedUser.Identities[0])
	})

	// Test UserAccount with UserID containing invalid chars
	t.Run("create or update identities from UserID with invalid chars OK", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		userAcc.Spec.UserID = "01234:ABCDEF"

		t.Run("create user identity mapping", func(t *testing.T) {
			r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingUser)
			//when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			// User is expected to be created in first reconcile
			updatedUser := assertUser(t, r, userAcc)

			t.Run("create first identity", func(t *testing.T) {
				r, req, _, _ := prepareReconcile(t, username, userAcc, updatedUser)
				//when
				res, err := r.Reconcile(context.TODO(), req)

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
				identity1 := assertIdentity(t, r, userAcc, config.Auth().Idp())

				t.Run("reconcile once more to ensure the user-identity mapping", func(t *testing.T) {
					r, req, _, _ := prepareReconcile(t, username, userAcc, updatedUser, identity1)
					//when
					res, err := r.Reconcile(context.TODO(), req)
					//then
					require.NoError(t, err)
					assert.Equal(t, reconcile.Result{}, res)

					// Check the user identity mapping
					checkMapping(t, updatedUser, identity1)
				})
			})
		})

	})

	// Test existing User and Identity without provider label
	t.Run("existing User without provider label has the label added", func(t *testing.T) {

		t.Run("User with nil labels", func(t *testing.T) {
			// given
			withoutAnyLabel := preexistingUser.DeepCopy()
			withoutAnyLabel.Labels = nil
			r, req, _, _ := prepareReconcile(t, username, userAcc, withoutAnyLabel, preexistingIdentity)

			// when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			assertUser(t, r, userAcc)
		})

		t.Run("User with another label defined", func(t *testing.T) {
			// given
			withoutLabel := preexistingUser.DeepCopy()
			delete(withoutLabel.Labels, toolchainv1alpha1.ProviderLabelKey)
			r, req, _, _ := prepareReconcile(t, username, userAcc, withoutLabel, preexistingIdentity)

			// when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			assertUser(t, r, userAcc)
		})
	})

	t.Run("existing Identity without provider label has the label added", func(t *testing.T) {

		t.Run("Identity with nil labels", func(t *testing.T) {
			// given
			withoutLabel := preexistingIdentity.DeepCopy()
			withoutLabel.Labels = nil
			r, req, _, _ := prepareReconcile(t, username, userAcc, withoutLabel, preexistingUser)

			// when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			assertIdentity(t, r, userAcc, config.Auth().Idp())
		})

		t.Run("Identity with another label defined", func(t *testing.T) {
			// given
			withoutLabel := preexistingIdentity.DeepCopy()
			delete(withoutLabel.Labels, toolchainv1alpha1.ProviderLabelKey)
			r, req, _, _ := prepareReconcile(t, username, userAcc, withoutLabel, preexistingUser)

			// when
			res, err := r.Reconcile(context.TODO(), req)

			//then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			assertIdentity(t, r, userAcc, config.Auth().Idp())
		})
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
		userAcc := newUserAccount(username, userID)
		fakeClient := fake.NewClientBuilder().WithObjects(userAcc).Build()
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
		userAcc := newUserAccount(username, userID)
		fakeClient := fake.NewClientBuilder().WithObjects(userAcc).Build()
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
		// Status is not updated
		useraccount.AssertThatUserAccount(t, username, fakeClient).HasNoConditions()
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID)
		fakeClient := fake.NewClientBuilder().WithObjects(userAcc).Build()
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
	os.Setenv("WATCH_NAMESPACE", test.MemberOperatorNs)
	username := "johndoe"
	userID := uuid.NewV4().String()
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	config, err := membercfg.GetConfiguration(test.NewFakeClient(t))
	require.NoError(t, err)

	userAcc := newUserAccount(username, userID)
	userUID := types.UID(username + "user")
	preexistingIdentity := &userv1.Identity{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
			UID:    types.UID(username + "identity"),
			Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
		},
		User: corev1.ObjectReference{
			Name: username,
			UID:  userUID,
		},
	}
	preexistingUser := &userv1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:   username,
			UID:    userUID,
			Labels: map[string]string{"toolchain.dev.openshift.com/owner": username},
		},
		Identities: []string{
			ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp()),
		},
	}
	preexistingNsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userAcc.Name,
			Namespace: test.MemberOperatorNs,
		},
		Spec: newNSTmplSetSpec(),
		Status: toolchainv1alpha1.NSTemplateSetStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionTrue,
				},
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

		res, err := r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		//then
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabling", "deleting user/identity"))

		res, err = r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).Exists()
	})

	t.Run("disabled useraccount", func(t *testing.T) {
		userAcc := newUserAccount(username, userID, disabled())

		// given
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingNsTmplSet)

		res, err := r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabled", ""))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).Exists()
	})

	t.Run("disabled useraccount - ignoring user and identity without owner label", func(t *testing.T) {
		userAcc := newUserAccount(username, userID, disabled())
		identityWithoutLabel := preexistingIdentity.DeepCopy()
		identityWithoutLabel.Labels = nil
		userWithoutLabel := preexistingUser.DeepCopy()
		userWithoutLabel.Labels = nil

		// given
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingNsTmplSet, identityWithoutLabel, userWithoutLabel)

		res, err := r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabled", ""))

		// identity & user without label stay there
		identity := &userv1.Identity{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: ToIdentityName(userAcc.Spec.UserID, config.Auth().Idp())}, identity)
		require.NoError(t, err)
		user := &userv1.User{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)

		// Check NSTemplate
		AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).Exists()
	})

	t.Run("disabling useraccount without user", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, disabled())
		r, req, cl, _ := prepareReconcile(t, username, userAcc, preexistingIdentity, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		// then
		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).Exists()
	})

	t.Run("disabling useraccount without identity", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, disabled())
		r, req, cl, config := prepareReconcile(t, username, userAcc, preexistingUser, preexistingNsTmplSet)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		assert.Equal(t, reconcile.Result{}, res)
		require.NoError(t, err)

		useraccount.AssertThatUserAccount(t, req.Name, cl).
			HasConditions(notReady("Disabling", "deleting user/identity"))

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		// Check that the associated user has been deleted
		// since disabled has been set to true
		assertUserNotFound(t, r, userAcc)

		// Check NSTemplate
		AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).Exists()
	})

	t.Run("deleting identity for disabled useraccount", func(t *testing.T) {
		// given
		userAcc := newUserAccount(username, userID, disabled(), withFinalizer())
		userAcc.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		r, req, _, _ := prepareReconcile(t, username, userAcc, preexistingNsTmplSet, preexistingUser, preexistingIdentity)

		// when
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)

		// Check that the associated identity has been deleted
		// since disabled has been set to true
		assertIdentityNotFound(t, r, userAcc, config.Auth().Idp())

		t.Run("deleting user for disabled useraccount", func(t *testing.T) {
			// when
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)

			// Check that the associated user has been deleted
			// since disabled has been set to true
			assertUserNotFound(t, r, userAcc)

			t.Run("deleting NSTemplateSet for disabled useraccount", func(t *testing.T) {
				// when
				_, err := r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)

				// Check that the associated NSTemplateSet has been deleted
				AssertThatNSTemplateSet(t, userAcc.Namespace, userAcc.Name, r.Client).DoesNotExist()

				t.Run("removing finalizer for disabled useraccount", func(t *testing.T) {
					// when
					_, err := r.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					useraccount.AssertThatUserAccount(t, username, r.Client).HasNoFinalizer()
				})
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
		userAcc := newUserAccount(username, userID)
		r, _, _, config := prepareReconcile(t, username, userAcc, cheRoute(true), keycloackRoute(true))

		// when
		err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

		// then
		require.NoError(t, err)
	})

	t.Run("che user deletion is enabled", func(t *testing.T) {
		memberOperatorSecret := newSecretWithCheAdminCreds()

		cfg := commonconfig.NewMemberOperatorConfigWithReset(t,
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
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, cfg, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			//when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.EqualError(t, err, `request to find Che user 'sugar' failed: unable to obtain access token for che, Response status: '400 Bad Request'. Response body: ''`)
			useraccount.AssertThatUserAccount(t, username, r.Client).HasNoConditions()
			require.Empty(t, userAcc.Status.Conditions)
			require.Equal(t, 1, *mockCallsCounter) // 1. get token
		})

		t.Run("user not found", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 404, mockCallsCounter)
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, username, r.Client).HasNoConditions()
			require.Equal(t, 2, *mockCallsCounter) // 1. get token 2. user exists check
		})

		t.Run("find user error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 400, mockCallsCounter)
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.EqualError(t, err, `request to find Che user 'sugar' failed, Response status: '400 Bad Request' Body: ''`)
			useraccount.AssertThatUserAccount(t, username, r.Client).HasNoConditions()
			require.Equal(t, 2, *mockCallsCounter) // 1. get token 2. user exists check
		})

		t.Run("find user ID parse error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserNoBody(username, 200, mockCallsCounter)
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.EqualError(t, err, `unable to get Che user ID for user 'sugar': error unmarshalling Che user json  : unexpected end of JSON input`)
			useraccount.AssertThatUserAccount(t, username, r.Client).HasNoConditions()
			require.Equal(t, 3, *mockCallsCounter) // 1. get token 2. user exists check 3. get user ID
		})

		t.Run("delete error", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserTimes(username, 2, mockCallsCounter)
			gockDeleteUser(400, mockCallsCounter)
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.EqualError(t, err, `this error is expected if deletion is still in progress: unable to delete Che user with ID 'abc1234', Response status: '400 Bad Request' Body: ''`)
			useraccount.AssertThatUserAccount(t, username, r.Client).Exists()
			require.Equal(t, 4, *mockCallsCounter) // 1. get token 2. check user exists 3. get user ID 4. delete user
		})

		t.Run("successful lookup and delete", func(t *testing.T) {
			// given
			mockCallsCounter := new(int)
			defer gock.OffAll()
			gockTokenSuccess(mockCallsCounter)
			gockFindUserTimes(username, 2, mockCallsCounter)
			gockDeleteUser(204, mockCallsCounter)
			userAcc := newUserAccount(username, userID)
			r, _, _, config := prepareReconcile(t, username, userAcc, memberOperatorSecret, cheRoute(true), keycloackRoute(true))

			// when
			err := r.lookupAndDeleteCheUser(logf.Log, config, userAcc)

			// then
			require.NoError(t, err)
			useraccount.AssertThatUserAccount(t, username, r.Client).HasNoConditions()
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

func assertUser(t *testing.T, r *Reconciler, userAcc *toolchainv1alpha1.UserAccount) *userv1.User {
	user := &userv1.User{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
	require.NoError(t, err)
	require.NotNil(t, user.Labels)
	assert.Equal(t, userAcc.Name, user.Labels["toolchain.dev.openshift.com/owner"])
	assert.Equal(t, toolchainv1alpha1.ProviderLabelValue, user.Labels[toolchainv1alpha1.ProviderLabelKey])
	assert.Empty(t, user.OwnerReferences) // User has no explicit owner reference.// Check the user identity mapping
	return user
}

func assertIdentityNotFound(t *testing.T, r *Reconciler, userAcc *toolchainv1alpha1.UserAccount, idp string) {
	identityName := ToIdentityName(userAcc.Spec.UserID, idp)
	// Check that the associated identity has been deleted
	// since disabled has been set to true
	identity := &userv1.Identity{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity)
	require.Error(t, err)
	assert.True(t, apierros.IsNotFound(err))
}

func assertIdentity(t *testing.T, r *Reconciler, userAcc *toolchainv1alpha1.UserAccount, idp string) *userv1.Identity {
	identity := &userv1.Identity{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: identity2.NewIdentityNamingStandard(userAcc.Spec.UserID, idp).IdentityName()}, identity)
	require.NoError(t, err)
	require.NotNil(t, identity.Labels)
	assert.Equal(t, userAcc.Name, identity.Labels["toolchain.dev.openshift.com/owner"])
	assert.Equal(t, toolchainv1alpha1.ProviderLabelValue, identity.Labels[toolchainv1alpha1.ProviderLabelKey])
	assert.Empty(t, identity.OwnerReferences) // User has no explicit owner reference.// Check the user identity mapping
	return identity
}

type userAccountOption func(*toolchainv1alpha1.UserAccount)

func disabled() userAccountOption {
	return func(userAcc *toolchainv1alpha1.UserAccount) {
		userAcc.Spec.Disabled = true
	}
}

func withFinalizer() userAccountOption {
	return func(userAcc *toolchainv1alpha1.UserAccount) {
		userAcc.Finalizers = []string{toolchainv1alpha1.FinalizerName}
	}
}

func withReadyCondition(reason string) userAccountOption {
	return func(userAcc *toolchainv1alpha1.UserAccount) {
		userAcc.Status = toolchainv1alpha1.UserAccountStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{
					Type:               toolchainv1alpha1.ConditionReady,
					Status:             corev1.ConditionTrue,
					Reason:             reason,
					LastTransitionTime: metav1.Now(),
				},
			},
		}
	}
}

func withNotReadyCondition(reason string) userAccountOption {
	return func(userAcc *toolchainv1alpha1.UserAccount) {
		userAcc.Status = toolchainv1alpha1.UserAccountStatus{
			Conditions: []toolchainv1alpha1.Condition{
				{
					Type:               toolchainv1alpha1.ConditionReady,
					Status:             corev1.ConditionFalse,
					Reason:             reason,
					LastTransitionTime: metav1.Now(),
				},
			},
		}
	}
}

func withoutNSTemplateSet() userAccountOption {
	return func(userAcc *toolchainv1alpha1.UserAccount) {
		userAcc.Spec.NSTemplateSet = nil
	}
}

func newUserAccount(userName, userID string, opts ...userAccountOption) *toolchainv1alpha1.UserAccount {
	tmplSet := newNSTmplSetSpec()
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: test.MemberOperatorNs,
			UID:       types.UID(uuid.NewV4().String()),
			Labels: map[string]string{
				toolchainv1alpha1.TierLabelKey: "basic",
			},
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID: userID,
			UserAccountSpecBase: toolchainv1alpha1.UserAccountSpecBase{
				NSTemplateSet: &tmplSet,
			},
		},
	}
	for _, apply := range opts {
		apply(userAcc)
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

func checkMapping(t *testing.T, user *userv1.User, identities ...*userv1.Identity) {
	require.Len(t, user.Identities, len(identities))

	for i, identity := range identities {
		assert.Equal(t, user.Name, identity.User.Name)
		assert.Equal(t, user.UID, identity.User.UID)
		assert.Equal(t, identity.Name, user.Identities[i])
	}
}

func prepareReconcile(t *testing.T, username string, initObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient, membercfg.Configuration) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)
	config, err := membercfg.GetConfiguration(fakeClient)
	require.NoError(t, err)

	tc := che.NewTokenCache(http.DefaultClient)
	cheClient := che.NewCheClient(http.DefaultClient, fakeClient, tc)

	r := &Reconciler{
		Client:    fakeClient,
		Scheme:    s,
		CheClient: cheClient,
	}
	return r, newReconcileRequest(username), fakeClient, config
}

func notReady(reason, msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: msg,
	}
}

func updating() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: "Updating",
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

func terminating(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.UserAccountTerminatingReason,
		Message: msg,
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

func newSecretWithCheAdminCreds() *corev1.Secret {
	return &corev1.Secret{
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

func gockFindUserTimes(name string, times int, calls *int) { //nolint: unparam
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
