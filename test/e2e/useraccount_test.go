package e2e

import (
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 60
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func TestUserAccount(t *testing.T) {
	userAccList := &toolchainv1alpha1.UserAccountList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "UserAccount",
			APIVersion: "toolchain.dev.openshift.com/v1alpha1",
		},
	}

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

	// wait for toolchain-operator to be ready
	err = e2eutil.WaitForDeployment(t, f.KubeClient, namespace, "member-operator", 1, retryInterval, timeout)
	require.NoError(t, err, "failed while waiting for operator deployment")

	t.Log("member-operator is ready and running state")
}
