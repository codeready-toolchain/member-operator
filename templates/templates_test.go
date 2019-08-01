package templates_test

import (
	"os"
	"testing"

	"github.com/codeready-toolchain/member-operator/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate(t *testing.T) {
	param := make(map[string]string)
	param["USER_NAME"] = "johnsmith"
	// param["COMMIT"] = "1234567"
	param["PROJECT_REQUESTING_USER"] = "admin"

	f, err := os.Create("/tmp/dat2")
	require.NoError(t, err)

	err = templates.ProcessTemplate(f, "templates/basic-user-template.yml", param)
	assert.NoError(t, err)
}
