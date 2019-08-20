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
		require.Len(t, tmplType.Templates, 1)
		assert.Equal(t, "basic", tmplType.Templates[0].Type)
	})

	t.Run("not found", func(t *testing.T) {
		// when
		tmplType, found := templates.GetTemplate("not_found")
		// then
		assert.False(t, found)
		assert.Empty(t, tmplType)
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

	t.Run("basic-user-template-not-found", func(t *testing.T) {
		// when
		_, err := templates.GetTemplateContent("basic-user-template1.yml")
		// then
		require.Error(t, err)
		assert.EqualError(t, err, "Asset pkg/templates/basic-user-template1.yml not found")
	})
}
