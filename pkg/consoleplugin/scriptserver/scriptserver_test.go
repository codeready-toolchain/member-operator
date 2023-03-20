package scriptserver

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScriptServer(t *testing.T) {
	s := NewScriptServer()

	body := handleScriptRequest(t, s, "/pendo.ts")
	require.Len(t, body, 1934)
	require.True(t, strings.HasPrefix(body, "// initialize pendo"))

}

func TestHealthStatusEndpoint(t *testing.T) {
	s := NewScriptServer()

	status := handleScriptRequest(t, s, "/status")
	pluginManifest := handleScriptRequest(t, s, "/plugin-manifest.json")

	assert.NotEmpty(t, status)
	assert.Equal(t, status, pluginManifest)
}

func handleScriptRequest(t *testing.T, server ScriptServer, path string) string {
	req := httptest.NewRequest("GET", path, nil)
	resp := httptest.NewRecorder()

	server.HandleScriptRequest(resp, req)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.Code)
	return string(body)
}
