package contentserver_test

import (
	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/contentserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	DefaultConfig = NewContentServerTestConfig("ABC", "cdn.pendo.io")
)

type ContentServerTestConfig struct {
	pendoKey  string
	pendoHost string
}

func (c *ContentServerTestConfig) PendoKey() string {
	return c.pendoKey
}

func (c *ContentServerTestConfig) PendoHost() string {
	return c.pendoHost
}

func NewContentServerTestConfig(pendoKey, pendoHost string) *ContentServerTestConfig {
	return &ContentServerTestConfig{
		pendoKey:  pendoKey,
		pendoHost: pendoHost,
	}
}

func TestContentServer(t *testing.T) {
	s := contentserver.NewContentServer(NewContentServerTestConfig("9473265123", "cdn.pendo.io"))

	body := handleScriptRequest(t, s, "/plugin-manifest.json")
	require.Len(t, body, 397)
	require.True(t, strings.HasPrefix(body, "{\n  \"name\": \"toolchain-member-web-console-plugin\","))
	require.True(t, strings.HasSuffix(strings.TrimSpace(body), "}"))

	body = handleScriptRequest(t, s, "/plugin-entry.js")
	require.Len(t, body, 2970)
	require.True(t, strings.HasPrefix(body, "window.loadPluginEntry("))
}

func TestHealthStatusEndpoint(t *testing.T) {
	s := contentserver.NewContentServer(DefaultConfig)

	status := handleScriptRequest(t, s, "/status")
	pluginManifest := handleScriptRequest(t, s, "/plugin-manifest.json")

	assert.NotEmpty(t, status)
	assert.Equal(t, status, pluginManifest)
}

func handleScriptRequest(t *testing.T, server contentserver.ContentServer, path string) string {
	req := httptest.NewRequest("GET", path, nil)
	resp := httptest.NewRecorder()

	server.HandleContentRequest(resp, req)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.Code)
	return string(body)
}
