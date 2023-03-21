package contentserver

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
	s := NewContentServer()

	body := handleScriptRequest(t, s, "/pendo.ts")
	require.Len(t, body, 1934)
	require.True(t, strings.HasPrefix(body, "// initialize pendo"))

}

func TestHealthStatusEndpoint(t *testing.T) {
	s := NewContentServer()

	status := handleScriptRequest(t, s, "/status")
	pluginManifest := handleScriptRequest(t, s, "/plugin-manifest.json")

	assert.NotEmpty(t, status)
	assert.Equal(t, status, pluginManifest)
}

func handleScriptRequest(t *testing.T, server ContentServer, path string) string {
	req := httptest.NewRequest("GET", path, nil)
	resp := httptest.NewRecorder()

	server.HandleContentRequest(resp, req)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Len(t, body, 1934)
	require.True(t, strings.HasPrefix(string(body), "// initialize pendo"))

	assert.Equal(t, http.StatusOK, resp.Code)
	return string(body)
}
