package memberoperatorconfig

import (
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
)

type MemberOperatorConfig struct {
	cfg *toolchainv1alpha1.MemberOperatorConfigSpec
}

func (c *MemberOperatorConfig) MemberStatus() MemberStatusConfig {
	return MemberStatusConfig{c.cfg.MemberStatus}
}

type MemberStatusConfig struct {
	memberStatus toolchainv1alpha1.MemberStatusConfig
}

func (a MemberStatusConfig) RefreshPeriod() time.Duration {
	defaultRefreshPeriod := "5s"
	refreshPeriod := getString(a.memberStatus.RefreshPeriod, defaultRefreshPeriod)
	d, err := time.ParseDuration(refreshPeriod)
	if err != nil {
		d, _ = time.ParseDuration(defaultRefreshPeriod)
	}
	return d
}

func getString(value *string, defaultValue string) string {
	if value != nil {
		return *value
	}
	return defaultValue
}
