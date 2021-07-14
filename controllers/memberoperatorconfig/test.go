package memberoperatorconfig

import (
	"context"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewMemberOperatorConfigWithReset(t *testing.T, options ...testconfig.MemberOperatorConfigOption) *toolchainv1alpha1.MemberOperatorConfig {
	t.Cleanup(Reset)
	return testconfig.NewMemberOperatorConfig(options...)
}

func UpdateMemberOperatorConfigWithReset(t *testing.T, cl client.Client, options ...testconfig.MemberOperatorConfigOption) *toolchainv1alpha1.MemberOperatorConfig {
	currentConfig := &toolchainv1alpha1.MemberOperatorConfig{}
	err := cl.Get(context.TODO(), types.NamespacedName{Namespace: test.MemberOperatorNs, Name: "config"}, currentConfig)
	require.NoError(t, err)
	t.Cleanup(Reset)
	return testconfig.ModifyMemberOperatorConfig(currentConfig, options...)
}
