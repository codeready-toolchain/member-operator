package nstemplateset

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	testmem "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func createTierTemplateRevision(templateRef string) *toolchainv1alpha1.TierTemplateRevision {
	return &toolchainv1alpha1.TierTemplateRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      templateRef,
			Namespace: test.HostOperatorNs,
		},
	}
}

func TestGetTTR(t *testing.T) {
	// given
	ttRev := createTierTemplateRevision("test-ttr")
	ttRevTest := createTierTemplateRevision("test-ttr-abc")
	ttRevExtra := createTierTemplateRevision("test-ttr-def")
	ctx := context.TODO()
	cl := test.NewFakeClient(t, ttRev, ttRevExtra, ttRevTest)
	hostCluster := testmem.NewGetHostCluster(cl, true, apiv1.ConditionTrue)
	t.Run("fetch ttr successfully", func(t *testing.T) {
		//when
		ttr, err := getToolchainTierTemplateRevision(ctx, hostCluster, "test-ttr")

		//then
		require.NoError(t, err)
		require.Equal(t, ttRev, ttr)
	})

	t.Run("host cluster not ready", func(t *testing.T) {
		//given
		hostCluster := testmem.NewGetHostCluster(cl, true, apiv1.ConditionFalse)
		//when
		_, err := getToolchainTierTemplateRevision(ctx, hostCluster, "test-ttr")
		//then
		require.EqualError(t, err, "the host cluster is not ready")

	})

	t.Run("host cluster not ok", func(t *testing.T) {
		noCluster := testmem.NewGetHostCluster(cl, false, apiv1.ConditionFalse)
		//when
		_, err := getToolchainTierTemplateRevision(ctx, noCluster, "test-ttr")
		//then
		require.EqualError(t, err, "unable to connect to the host cluster: unknown cluster")

	})

	t.Run("error while fetching ttr", func(t *testing.T) {
		//given
		cl.MockGet = func(ctx context.Context, key types.NamespacedName, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
			if _, ok := obj.(*toolchainv1alpha1.TierTemplateRevision); ok {
				return fmt.Errorf("mock error")
			}
			return cl.Client.Get(ctx, key, obj, opts...)
		}
		//when
		_, err := getToolchainTierTemplateRevision(ctx, hostCluster, "test-ttr")

		//then
		require.ErrorContains(t, err, "unable to retrieve the TierTemplateRevision")

	})

}
