package configuration

import (
	"os"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/gofrs/uuid"
	"github.com/spf13/cast"
	"github.com/stretchr/testify/require"
	"gotest.tools/assert"
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
	})
	t.Run("env overwrite", func(t *testing.T) {
		// given
		restore := test.SetEnvVarsAndRestore(t,
			test.Env("MEMBER_OPERATOR_CONFIG_MAP_NAME", "test-config"),
			test.Env("MEMBER_OPERATOR_IDENTITY_PROVIDER", ""),
			test.Env("MEMBER_OPERATOR_TEST_TEST", ""))
		defer restore()

		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "toolchain-member-operator",
			},
			Data: map[string]string{
				"identity.provider": "test-idp",
				"test-test":         "test-test",
			},
		}

		cl := test.NewFakeClient(t, configMap)

		// when
		config, err := LoadConfig(cl)

		// then
		require.NoError(t, err)
		assert.Equal(t, "test-idp", config.GetIdP())

		// test env vars are parsed and created correctly
		idpName := os.Getenv("MEMBER_OPERATOR_IDENTITY_PROVIDER")
		assert.Equal(t, idpName, "test-idp")
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
		assert.Equal(t, cast.ToDuration("3s"), config.GetClusterHealthCheckTimeout())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "30s"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("30s"), config.GetClusterHealthCheckTimeout())
	})
}

func TestGetClusterHealthCheckFailureThreshold(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_HEALTHCHECK_FAILURE_THRESHOLD"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, int64(3), config.GetClusterHealthCheckFailureThreshold())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "5"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, int64(5), config.GetClusterHealthCheckFailureThreshold())
	})
}

func TestGetClusterHealthCheckSuccessThreshold(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_HEALTHCHECK_SUCCESS_THRESHOLD"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, int64(1), config.GetClusterHealthCheckSuccessThreshold())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "3"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, int64(3), config.GetClusterHealthCheckSuccessThreshold())
	})
}

func TestGetClusterAvailableDelay(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_AVAILABLE_DELAY"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("20s"), config.GetClusterAvailableDelay())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "30s"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "40s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("30s"), config.GetClusterAvailableDelay())
	})
}

func TestGetClusterUnavailableDelay(t *testing.T) {
	key := MemberEnvPrefix + "_CLUSTER_UNAVAILABLE_DELAY"
	resetFunc := test.UnsetEnvVarAndRestore(t, key)
	defer resetFunc()

	t.Run("default", func(t *testing.T) {
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("60s"), config.GetClusterUnavailableDelay())
	})

	t.Run("env overwrite", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env(key, "30s"),
			test.Env(MemberEnvPrefix+"_"+"ANY_CONFIG", "20s"))
		defer restore()
		config := getDefaultConfiguration(t)
		assert.Equal(t, cast.ToDuration("30s"), config.GetClusterUnavailableDelay())
	})
}
