package memberoperatorconfig

import (
	"testing"
	"time"

	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"

	"github.com/stretchr/testify/assert"
)

func TestAuth(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t)
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.Equal(t, "rhd", memberOperatorCfg.Auth().IdP())
	})
	t.Run("non-default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t, testconfig.Auth().IdP("another"))
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.Equal(t, "another", memberOperatorCfg.Auth().IdP())
	})
}

func TestAutoscaler(t *testing.T) {
	t.Run("deploy", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.False(t, memberOperatorCfg.Autoscaler().Deploy())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Autoscaler().Deploy(true))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.True(t, memberOperatorCfg.Autoscaler().Deploy())
		})
	})
	t.Run("buffer memory", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Empty(t, memberOperatorCfg.Autoscaler().BufferMemory())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Autoscaler().BufferMemory("5GiB"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "5GiB", memberOperatorCfg.Autoscaler().BufferMemory())
		})
	})
	t.Run("buffer replicas", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 0, memberOperatorCfg.Autoscaler().BufferReplicas())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Autoscaler().BufferReplicas(2))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 2, memberOperatorCfg.Autoscaler().BufferReplicas())
		})
	})
}

func TestChe(t *testing.T) {
	t.Run("admin username", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)

			memberOperatorCfg := Configuration{m: &cfg.Spec}
			assert.Equal(t, "", memberOperatorCfg.Che().AdminUserName())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t,
				testconfig.Che().Secret().
					Ref("che-secret").
					CheAdminUsernameKey("che-admin-username"))

			cheSecretValues := make(map[string]string)
			cheSecretValues["che-admin-username"] = "super-admin"
			secrets := make(map[string]map[string]string)
			secrets["che-secret"] = cheSecretValues
			memberOperatorCfg := Configuration{m: &cfg.Spec, secrets: secrets}

			assert.Equal(t, "super-admin", memberOperatorCfg.Che().AdminUserName())
		})
	})
	t.Run("admin password", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)

			memberOperatorCfg := Configuration{m: &cfg.Spec}
			assert.Equal(t, "", memberOperatorCfg.Che().AdminPassword())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t,
				testconfig.Che().Secret().
					Ref("che-secret").
					CheAdminPasswordKey("che-admin-password"))

			cheSecretValues := make(map[string]string)
			cheSecretValues["che-admin-password"] = "passw0rd"
			secrets := make(map[string]map[string]string)
			secrets["che-secret"] = cheSecretValues
			memberOperatorCfg := Configuration{m: &cfg.Spec, secrets: secrets}

			assert.Equal(t, "passw0rd", memberOperatorCfg.Che().AdminPassword())
		})
	})
	t.Run("is required", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.False(t, memberOperatorCfg.Che().IsRequired())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Che().Required(true))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.True(t, memberOperatorCfg.Che().IsRequired())
		})
	})
	t.Run("is user deletion enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.False(t, memberOperatorCfg.Che().IsUserDeletionEnabled())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Che().UserDeletionEnabled(true))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.True(t, memberOperatorCfg.Che().IsUserDeletionEnabled())
		})
	})
	t.Run("keycloak route name", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "codeready", memberOperatorCfg.Che().KeycloakRouteName())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Che().KeycloakRouteName("keycloak"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "keycloak", memberOperatorCfg.Che().KeycloakRouteName())
		})
	})
	t.Run("namespace", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "codeready-workspaces-operator", memberOperatorCfg.Che().Namespace())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Che().Namespace("crw"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "crw", memberOperatorCfg.Che().Namespace())
		})
	})
	t.Run("route name", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "codeready", memberOperatorCfg.Che().RouteName())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Che().RouteName("crw"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "crw", memberOperatorCfg.Che().RouteName())
		})
	})
}

func TestMemberStatusConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t)
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.Equal(t, 5*time.Second, memberOperatorCfg.MemberStatus().RefreshPeriod())
	})
	t.Run("non-default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t, testconfig.MemberStatus().RefreshPeriod("10s"))
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.Equal(t, 10*time.Second, memberOperatorCfg.MemberStatus().RefreshPeriod())
	})
}
