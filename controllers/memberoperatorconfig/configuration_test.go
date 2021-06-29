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

		assert.Equal(t, "rhd", memberOperatorCfg.Auth().Idp())
	})
	t.Run("non-default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t, testconfig.Auth().Idp("another"))
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.Equal(t, "another", memberOperatorCfg.Auth().Idp())
	})
}

func TestAutoscaler(t *testing.T) {
	t.Run("deploy", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.True(t, memberOperatorCfg.Autoscaler().Deploy())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Autoscaler().Deploy(false))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.False(t, memberOperatorCfg.Autoscaler().Deploy())
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

			assert.Equal(t, 1, memberOperatorCfg.Autoscaler().BufferReplicas())
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

func TestConsole(t *testing.T) {
	t.Run("console namespace", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "openshift-console", memberOperatorCfg.Console().Namespace())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Console().Namespace("another-namespace"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "another-namespace", memberOperatorCfg.Console().Namespace())
		})
	})
	t.Run("console route", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "console", memberOperatorCfg.Console().RouteName())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.Console().RouteName("another-route"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, "another-route", memberOperatorCfg.Console().RouteName())
		})
	})
}

func TestMemberStatus(t *testing.T) {
	t.Run("member status refresh period", func(t *testing.T) {
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
	})
}

func TestToolchainCluster(t *testing.T) {
	t.Run("health check period", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 10*time.Second, memberOperatorCfg.ToolchainCluster().HealthCheckPeriod())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.ToolchainCluster().HealthCheckPeriod("3s"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 3*time.Second, memberOperatorCfg.ToolchainCluster().HealthCheckPeriod())
		})
	})
	t.Run("health check timeout", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t)
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 3*time.Second, memberOperatorCfg.ToolchainCluster().HealthCheckTimeout())
		})
		t.Run("non-default", func(t *testing.T) {
			cfg := NewMemberOperatorConfigWithReset(t, testconfig.ToolchainCluster().HealthCheckTimeout("11s"))
			memberOperatorCfg := Configuration{m: &cfg.Spec}

			assert.Equal(t, 11*time.Second, memberOperatorCfg.ToolchainCluster().HealthCheckTimeout())
		})
	})
}

func TestWebhook(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t)
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.True(t, memberOperatorCfg.Webhook().Deploy())
	})
	t.Run("non-default", func(t *testing.T) {
		cfg := NewMemberOperatorConfigWithReset(t, testconfig.Webhook().Deploy(false))
		memberOperatorCfg := Configuration{m: &cfg.Spec}

		assert.False(t, memberOperatorCfg.Webhook().Deploy())
	})
}
