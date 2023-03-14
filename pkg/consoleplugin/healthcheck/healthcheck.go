package healthcheck

import (
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	log = logf.Log.WithName("web_console_health_check")
)

func HandleHealthCheck(w http.ResponseWriter, _ *http.Request) {
	respBody := []byte("OK")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}
