package templates_test

import (
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTemplate(t *testing.T) {

	t.Run("basic", func(t *testing.T) {
		// when
		tmplType, found := templates.GetTemplate("basic")
		// then
		require.True(t, found)
		assert.Equal(t, "basic", tmplType.Name)
	})
}

func TestGetTemplateContent(t *testing.T) {

	t.Run("basic-user-template", func(t *testing.T) {
		// when
		content, err := templates.GetTemplateContent("basic-user-template.yml")
		// then
		require.NoError(t, err)
		assert.NotEmpty(t, content)
	})
}
