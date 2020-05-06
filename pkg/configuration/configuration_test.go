package configuration

import (
	"os"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"gotest.tools/assert"

	"github.com/gofrs/uuid"
	"github.com/spf13/cast"
	"github.com/stretchr/testify/require"
)

// getDefaultConfiguration returns a configuration initialized without anything but
// defaults set. Remember that environment variables can overwrite defaults, so
// please ensure to properly unset envionment variables using
// UnsetEnvVarAndRestore().
func getDefaultConfiguration(t *testing.T) *Config {
	config := LoadConfig()
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
		key := "MEMBER_OPERATOR_IDENTITY_PROVIDER"
		resetFunc := test.UnsetEnvVarAndRestore(t, key)
		defer resetFunc()
		u, err := uuid.NewV4()
		require.NoError(t, err)
		os.Setenv(key, u.String())
		config := getDefaultConfiguration(t)
		params := config.GetAllMemberParameters()
		expected := make(map[string]string, 1)
		expected[key] = u.String()
		require.EqualValues(t, expected, params)
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
