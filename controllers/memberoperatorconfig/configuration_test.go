package memberoperatorconfig

import (
	"testing"
	"time"

	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"

	"github.com/stretchr/testify/assert"
)

func TestMemberStatusConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := newMemberOperatorConfigWithReset(t)
		memberOperatorCfg := MemberOperatorConfig{cfg: &cfg.Spec}

		assert.Equal(t, 5*time.Second, memberOperatorCfg.MemberStatus().RefreshPeriod())
	})
	t.Run("non-default", func(t *testing.T) {
		cfg := newMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
		memberOperatorCfg := MemberOperatorConfig{cfg: &cfg.Spec}

		assert.Equal(t, 10*time.Second, memberOperatorCfg.MemberStatus().RefreshPeriod())
	})
}
