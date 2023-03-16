package consoleplugin

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestHealthCheckServer(t *testing.T) {

	log := ctrl.Log.WithName("test")

	s := NewConsolePluginHealthServer(log)

	s.Start()
	//defer s.Shutdown(context.Background())

	cl := http.Client{}

	// Test the health check endpoint
	resp, err := cl.Get("http://localhost:8080/api/v1/status")
	if resp != nil {
		defer resp.Body.Close()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Confirm we get a not found for a bad request
	resp, err = cl.Get("http://localhost:8080/foo")
	if resp != nil {
		defer resp.Body.Close()
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
