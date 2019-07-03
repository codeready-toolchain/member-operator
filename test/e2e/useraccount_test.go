package e2e

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	userv1 "github.com/openshift/api/user/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	err = e2eutil.WaitForDeployment(t, f.KubeClient, namespace, "member-operator", 1, retryInterval, timeout)
	require.NoError(t, err, "failed while waiting for operator deployment")

	t.Log("member-operator is ready and running state")

	// create useraccount
	userAcc := newUserAcc(t, f, ctx)
	err = f.Client.Create(context.TODO(), userAcc, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	require.NoError(t, err)
	t.Logf("user account '%s' created", userAcc.Name)

	t.Run("verify_useraccount_ok", func(t *testing.T) {
		err := verifyResources(t, f, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_user_ok", func(t *testing.T) {
		user := &userv1.User{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: userAcc.Name}, user)
		require.NoError(t, err)

		err = client.Delete(context.TODO(), user)
		require.NoError(t, err)

		err = verifyResources(t, f, userAcc)
		assert.NoError(t, err)
	})

	t.Run("delete_identity_ok", func(t *testing.T) {
		identity := &userv1.Identity{}
		err := client.Get(context.TODO(), types.NamespacedName{Name: getIdentityName(userAcc)}, identity)
		require.NoError(t, err)

		err = client.Delete(context.TODO(), identity)
		require.NoError(t, err)

		err = verifyResources(t, f, userAcc)
		assert.NoError(t, err)
	})
}

func newUserAcc(t *testing.T, f *framework.Framework, ctx *framework.TestCtx) *toolchainv1alpha1.UserAccount {
	name := "johnsmith"
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "toolchain-member-operator",
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID:  "1a03ecac-7c0b-44fc-b66d-12dd7fb21c40",
			NSLimit: "admin",
			NSTemplateSet: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					toolchainv1alpha1.Namespace{Type: "ide", Revision: "ab12ef"},
					toolchainv1alpha1.Namespace{Type: "cicd", Revision: "34efcd"},
					toolchainv1alpha1.Namespace{Type: "stage", Revision: "cdef56"},
				},
			},
		},
	}
	return userAcc
}

func verifyResources(t *testing.T, f *framework.Framework, userAcc *toolchainv1alpha1.UserAccount) error {
	if err := waitForUser(t, f.Client.Client, userAcc.Name); err != nil {
		return err
	}
	if err := waitForIdentity(t, f.Client.Client, getIdentityName(userAcc)); err != nil {
		return err
	}
	if err := waitForMapping(t, f.Client.Client, userAcc.Name, getIdentityName(userAcc)); err != nil {
		return err
	}
	return nil
}

func getIdentityName(userAcc *toolchainv1alpha1.UserAccount) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userAcc.Spec.UserID)
}
