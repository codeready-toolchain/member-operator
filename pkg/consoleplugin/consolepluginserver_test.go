package consoleplugin

import (
	"github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	DefaultRetryInterval = time.Millisecond * 100 // make it short because a "retry interval" is waited before the first test
	DefaultTimeout       = time.Second * 30
)

func TestConsolePluginServer(t *testing.T) {

	log := ctrl.Log.WithName("test")

	cfg := memberoperatorconfig.WebConsolePluginConfig{}

	s := NewConsolePluginServer(cfg, log, ConsolePluginServerOptionNoTLS)

	s.Start()
	waitForReady(t)

	cl := http.Client{}

	// Confirm we get a not found for a bad request
	resp, err := cl.Get("http://localhost:9443/foo")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Confirm that the script server correctly returns its resources
	resp, err = cl.Get("http://localhost:9443/pendo.ts")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, body, 1898)
	require.True(t, strings.HasPrefix(string(body), "// initialize pendo"))
}

func waitForReady(t *testing.T) {
	cl := http.Client{}

	err := wait.Poll(DefaultRetryInterval, DefaultTimeout, func() (done bool, err error) {
		req, err := http.NewRequest("GET", "http://localhost:9443/status", nil)
		if err != nil {
			return false, err
		}

		resp, err := cl.Do(req)
		defer func() {
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
		if err != nil {
			// We will ignore and try again until we don't get any error or timeout.
			return false, nil // nolint:nilerr
		}

		if resp.StatusCode != 200 {
			return false, nil
		}

		return true, nil
	})
	require.NoError(t, err)
}
