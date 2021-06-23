package memberoperatorconfig

import (
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"
)

func NewMemberOperatorConfigWithReset(t *testing.T, options ...testconfig.MemberOperatorConfigOption) *toolchainv1alpha1.MemberOperatorConfig {
	t.Cleanup(Reset)
	return testconfig.NewMemberOperatorConfig(options...)
}
