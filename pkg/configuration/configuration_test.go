package configuration

import (
	"os"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/gofrs/uuid"
	"github.com/spf13/cast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// getDefaultConfiguration returns a configuration initialized without anything but
// defaults set. Remember that environment variables can overwrite defaults, so
// please ensure to properly unset environment variables using
// UnsetEnvVarAndRestore().
func getDefaultConfiguration(t *testing.T) *Config {
	config, err := LoadConfig(test.NewFakeClient(t))
	require.NoError(t, err)
	require.NotNil(t, config)
	return config
}

func TestLoadConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		getDefaultConfiguration(t)
	})
}

func TestLoadFromSecret(t *testing.T) {
	namespaceName := "toolchain-member"
	restore := test.SetEnvVarAndRestore(t, "WATCH_NAMESPACE", namespaceName)
	defer restore()

	t.Run("default", func(t *testing.T) {
		// when
		config := getDefaultConfiguration(t)

		// then
		assert.Equal(t, "", config.GetCheAdminUsername())
		assert.Equal(t, "", config.GetCheAdminPassword())
	})

	t.Run("env overwrite", func(t *testing.T) {
		// given
		restore := test.SetEnvVarAndRestore(t, "MEMBER_OPERATOR_SECRET_NAME", "test-secret")
		defer restore()

		testSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespaceName,
			},
			Data: map[string][]byte{
				"che.admin.username": []byte("test-che-user"),
				"che.admin.password": []byte("test-che-password"),
			},
		}

		cl := test.NewFakeClient(t, testSecret)

		// when
		config, err := LoadConfig(cl)

		// then
		require.NoError(t, err)
		require.Equal(t, "test-che-user", config.GetCheAdminUsername())
		require.Equal(t, "test-che-password", config.GetCheAdminPassword())
	})

	t.Run("secret not found", func(t *testing.T) {
		// given
		restore := test.SetEnvVarAndRestore(t, "MEMBER_OPERATOR_SECRET_NAME", "test-secret")
		defer restore()

		cl := test.NewFakeClient(t)

		// when
		config, err := LoadConfig(cl)

		// then
		require.NoError(t, err)
		assert.NotNil(t, config)
		require.Empty(t, config.GetCheAdminUsername())
		require.Empty(t, config.GetCheAdminPassword())
	})
}

func TestGetAllMemberParameters(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		config := getDefaultConfiguration(t)
		params := config.GetAllMemberParameters()
		require.Empty(t, params)
	})
	t.Run("IdP environment variable", func(t *testing.T) {
		key := MemberEnvPrefix + "_IDENTITY_PROVIDER"
		u, err := uuid.NewV4()
		require.NoError(t, err)
		restore := test.SetEnvVarAndRestore(t, key, u.String())
		defer restore()
		config := getDefaultConfiguration(t)
		params := config.GetAllMemberParameters()
		expected := make(map[string]string, 1)
		expected[key] = u.String()
		require.EqualValues(t, expected, params)
	})
}

func TestLoadFromConfigMap(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, "WATCH_NAMESPACE", "toolchain-member-operator")
	defer restore()
	t.Run("default", func(t *testing.T) {
		// when
		config := getDefaultConfiguration(t)

		// then
		assert.Equal(t, "rhd", config.GetIdP())
		assert.Equal(t, "console", config.GetConsoleRouteName())
		assert.Equal(t, "openshift-console", config.GetConsoleNamespace())
		assert.Equal(t, "codeready", config.GetCheRouteName())
		assert.Equal(t, "codeready-workspaces-operator", config.GetCheNamespace())
	})
	t.Run("env overwrite", func(t *testing.T) {
		// given
		restore := test.SetEnvVarsAndRestore(t,
			test.Env("MEMBER_OPERATOR_CONFIG_MAP_NAME", "test-config"),
			test.Env("MEMBER_OPERATOR_IDENTITY_PROVIDER", ""),
			test.Env("MEMBER_OPERATOR_CONSOLE_NAMESPACE", ""),
			test.Env("MEMBER_OPERATOR_CONSOLE_ROUTE_NAME", ""),
			test.Env("MEMBER_OPERATOR_CHE_NAMESPACE", ""),
			test.Env("MEMBER_OPERATOR_CHE_ROUTE_NAME", ""),
			test.Env("MEMBER_OPERATOR_TEST_TEST", ""))
		defer restore()

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "toolchain-member-operator",
			},
			Data: map[string]string{
				"identity.provider":  "test-idp",
				"console.namespace":  "test-console-namespace",
				"console.route.name": "test-console-route-name",
				"che.namespace":      "test-che-namespace",
				"che.route.name":     "test-che-route-name",
				"test-test":          "test-test",
			},
		}

		cl := test.NewFakeClient(t, configMap)

		// when
		config, err := LoadConfig(cl)

		// then
		require.NoError(t, err)
		assert.Equal(t, "test-idp", config.GetIdP())
		assert.Equal(t, "test-console-namespace", config.GetConsoleNamespace())
		assert.Equal(t, "test-console-route-name", config.GetConsoleRouteName())
		assert.Equal(t, "test-che-namespace", config.GetCheNamespace())
		assert.Equal(t, "test-che-route-name", config.GetCheRouteName())

		// test env vars are parsed and created correctly
		idpName := os.Getenv("MEMBER_OPERATOR_IDENTITY_PROVIDER")
		assert.Equal(t, idpName, "test-idp")
		consoleNamespace := os.Getenv("MEMBER_OPERATOR_CONSOLE_NAMESPACE")
		assert.Equal(t, consoleNamespace, "test-console-namespace")
		consoleRouteName := os.Getenv("MEMBER_OPERATOR_CONSOLE_ROUTE_NAME")
		assert.Equal(t, consoleRouteName, "test-console-route-name")
		cheNamespace := os.Getenv("MEMBER_OPERATOR_CHE_NAMESPACE")
		assert.Equal(t, cheNamespace, "test-che-namespace")
		cheRouteName := os.Getenv("MEMBER_OPERATOR_CHE_ROUTE_NAME")
		assert.Equal(t, cheRouteName, "test-che-route-name")
		testTest := os.Getenv("MEMBER_OPERATOR_TEST_TEST")
		assert.Equal(t, testTest, "test-test")
	})

	t.Run("configMap not found", func(t *testing.T) {
		// given
		restore := test.SetEnvVarAndRestore(t, "MEMBER_OPERATOR_CONFIG_MAP_NAME", "test-config")
		defer restore()

		cl := test.NewFakeClient(t)

		// when
		config, err := LoadConfig(cl)

		// then
		require.NoError(t, err)
		require.NotNil(t, config)
	})
}

func TestGetIdP(t *testing.T) {
	key := MemberEnvPrefix + "_IDENTITY_PROVIDER"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, "rhd", config.GetIdP())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "testingIdP"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, "testingIdP", config.GetIdP())
	})
}

func TestGetMemberClusterName(t *testing.T) {
	key := MemberEnvPrefix + "_MEMBER_STATUS"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, "toolchain-member-status", config.GetMemberStatusName())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "testingMemberStatusName"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, "testingMemberStatusName", config.GetMemberStatusName())
	})
}

func TestGetClusterHealthCheckPeriod(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_HEALTHCHECK_PERIOD"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("10s"), config.GetClusterHealthCheckPeriod())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "30s"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("30s"), config.GetClusterHealthCheckPeriod())
	})
}

func TestGetClusterHealthCheckTimeout(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_HEALTHCHECK_TIMEOUT"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("3s"), config.GetToolchainClusterTimeout())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "30s"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("30s"), config.GetToolchainClusterTimeout())
	})
}

func TestGetMemberStatusRefreshTime(t *testing.T) {
	key := MemberEnvPrefix + "_" + "MEMBERSTATUS_REFRESH_TIME"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("5s"), config.GetMemberStatusRefreshTime())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarAndRestore(t, key, "1s")
		defer restore()

		restore = test.SetEnvVarAndRestore(t, MemberEnvPrefix+"_"+"ANY_CONFIG", "20s")
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("1s"), config.GetMemberStatusRefreshTime())
	})
}

func TestIsCheRequired(t *testing.T) {
	key := MemberEnvPrefix + "_" + "CHE_REQUIRED"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, false, config.IsCheRequired())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarAndRestore(t, key, "true")
		defer restore()

		restore = test.SetEnvVarAndRestore(t, MemberEnvPrefix+"_"+"ANY_CONFIG", "20s")
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, true, config.IsCheRequired())
	})
}

func TestGetMemberOperatorWebhookImage(t *testing.T) {
	key := MemberEnvPrefix + "_WEBHOOK_IMAGE"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Empty(t, config.GetMemberOperatorWebhookImage())
	})

	t.Run("from var", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "quay.io/cool/member-operator-webhook:123"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, "quay.io/cool/member-operator-webhook:123", config.GetMemberOperatorWebhookImage())
	})
}

func TestDoDeployWebhook(t *testing.T) {
	key := MemberEnvPrefix + "_DEPLOY_WEBHOOK"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.True(t, config.DoDeployWebhook())
	})

	t.Run("from var", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t, test.Env(key, "false"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.False(t, config.DoDeployWebhook())
	})
}
