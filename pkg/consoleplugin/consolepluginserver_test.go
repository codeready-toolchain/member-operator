package consoleplugin

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"strings"
	"testing"
)

func TestConsolePluginServer(t *testing.T) {

	log := ctrl.Log.WithName("test")

	s := NewConsolePluginServer(log)

	s.Start()
	defer s.Shutdown(context.Background())

	cl := http.Client{}

	// Test the health check endpoint
	resp, err := cl.Get("http://localhost:8080/api/v1/status")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Confirm we get a not found for a bad request
	resp, err = cl.Get("http://localhost:8080/foo")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Confirm that the script server correctly returns its resources
	resp, err = cl.Get("http://localhost:8080/pendo.ts")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, body, 1896)
	require.True(t, strings.HasPrefix(string(body), "// initialize pendo"))
}
