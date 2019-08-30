package e2e

import (
	"context"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccount"
	userv1 "github.com/openshift/api/user/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestUserAccount(t *testing.T) {
	userAccList := &toolchainv1alpha1.UserAccountList{}

	err := framework.AddToFrameworkScheme(apis.AddToScheme, userAccList)
	require.NoError(t, err, "failed to add custom resource scheme to framework")

	ctx := framework.NewTestCtx(t)
	defer ctx.Cleanup()

	err = ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	require.NoError(t, err, "failed to initialize cluster resources")
	t.Log("Initialized cluster resources")

	namespace, err := ctx.GetNamespace()
	require.NoError(t, err, "failed to get namespace where operator needs to run")

	// get global framework variables
	f := framework.Global
	client := f.Client.Client

	// wait for operator to be ready
	err = e2eutil.WaitForDeployment(t, f.KubeClient, namespace, "member-operator", 1, operatorRetryInterval, operatorTimeout)
	require.NoError(t, err, "failed while waiting for operator deployment")

	t.Log("member-operator is ready and running state")

	extraUserAcc := createUserAccount(t, f, ctx, "amar")
	t.Log("extra useraccount created at start")

	lucyUserAcc := createUserAccount(t, f, ctx, "lucy")
	t.Log("lucy useraccount created at start")

	// create useraccount
	userAcc := newUserAcc(namespace, "johnsmith")
	err = f.Client.Create(context.TODO(), userAcc, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	require.NoError(t, err)
	t.Logf("user account '%s' created", userAcc.Name)

	t.Run("verify_useraccount_ok", func(t *testing.T) {
		err := verifyResources(t, f, namespace, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_user_ok", func(t *testing.T) {
		user := &userv1.User{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)

		err = client.Delete(context.TODO(), user)
		require.NoError(t, err)

		err = verifyResources(t, f, namespace, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_identity_ok", func(t *testing.T) {
		identity := &userv1.Identity{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: useraccount.ToIdentityName(userAcc.Spec.UserID)}, identity)
		require.NoError(t, err)

		err = client.Delete(context.TODO(), identity)
		require.NoError(t, err)

		err = verifyResources(t, f, namespace, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_user_mapping_ok", func(t *testing.T) {
		user := &userv1.User{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)

		user.Identities = []string{}
		err = client.Update(context.TODO(), user)
		require.NoError(t, err)

		err = verifyResources(t, f, namespace, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_identity_mapping_ok", func(t *testing.T) {
		identity := &userv1.Identity{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: useraccount.ToIdentityName(userAcc.Spec.UserID)}, identity)
		require.NoError(t, err)

		identity.User = corev1.ObjectReference{Name: "", UID: ""}
		err = client.Update(context.TODO(), identity)
		require.NoError(t, err)

		err = verifyResources(t, f, namespace, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_useraccount_ok", func(t *testing.T) {
		lucyAcc := &toolchainv1alpha1.UserAccount{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: lucyUserAcc.Name, Namespace: namespace}, lucyAcc)
		assert.NoError(t, err)

		err = client.Delete(context.TODO(), lucyAcc)
		assert.NoError(t, err)

		err = waitForDeletedUserAccount(t, client, lucyAcc.Name, namespace)
		assert.NoError(t, err)

		err = waitForDeletedUser(t, client, lucyAcc.Name)
		assert.NoError(t, err)

		err = waitForDeletedIdentity(t, client, lucyAcc.Name)
		assert.NoError(t, err)
	})

	err = verifyResources(t, f, namespace, extraUserAcc)
	assert.NoError(t, err)
	t.Log("extra useraccount verified at end")
}

func newUserAcc(namespace, name string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID:  uuid.NewV4().String(),
			NSLimit: "admin",
			NSTemplateSet: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "ide", Revision: "ab12ef"},
					{Type: "cicd", Revision: "34efcd"},
					{Type: "stage", Revision: "cdef56"},
				},
			},
		},
	}
	return userAcc
}

func verifyResources(t *testing.T, f *framework.Framework, namespace string, userAcc *toolchainv1alpha1.UserAccount) error {
	if err := waitForUser(t, f.Client.Client, userAcc.Name); err != nil {
		return err
	}
	if err := waitForIdentity(t, f.Client.Client, useraccount.ToIdentityName(userAcc.Spec.UserID)); err != nil {
		return err
	}
	if err := waitForUserAccStatusConditions(t, f.Client.Client, namespace, userAcc.Name,
		toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
			Reason: "Provisioned",
		}); err != nil {
		return err
	}
	return nil
}

func createUserAccount(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, name string) *toolchainv1alpha1.UserAccount {
	namespace, err := ctx.GetNamespace()
	require.NoError(t, err)

	userAcc := newUserAcc(namespace, name)
	err = f.Client.Create(context.TODO(), userAcc, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	require.NoError(t, err)

	err = verifyResources(t, f, namespace, userAcc)
	assert.NoError(t, err)

	return userAcc
}
