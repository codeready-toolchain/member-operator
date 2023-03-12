package healthcheck

import (
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	log = logf.Log.WithName("web_console_health_check_webhook")
)

func HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	var respBody []byte
	respBody = []byte("OK")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}
