package configuration

import (
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
