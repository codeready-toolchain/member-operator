package scriptserver

import (
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	log = logf.Log.WithName("web_console_script_server_webhook")
)

func HandleScriptRequest(w http.ResponseWriter, r *http.Request) {
	var respBody []byte

	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(respBody); err != nil {
		log.Error(err, "unable to write response")
	}
}
