package scriptserver

import (
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScriptServer(t *testing.T) {
	s := NewScriptServer()

	req := httptest.NewRequest("GET", "/pendo.ts", nil)
	resp := httptest.NewRecorder()

	s.HandleScriptRequest(resp, req)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Len(t, body, 1896)
	require.True(t, strings.HasPrefix(string(body), "// initialize pendo"))

}
