package consoleplugin

import (
	"context"
	"net/http"
	"os"

	"github.com/codeready-toolchain/member-operator/pkg/cert"
	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/scriptserver"

	"github.com/go-logr/logr"
)

type Server struct {
	mux *http.ServeMux
	svr *http.Server
	log logr.Logger
}

func NewConsolePluginServer(log logr.Logger) Server {
	s := Server{
		log: log,
	}
	s.mux = http.NewServeMux()
	ss := scriptserver.NewScriptServer()
	s.mux.HandleFunc("/", ss.HandleScriptRequest)
	s.svr = &http.Server{ //nolint:gosec
		Addr:    ":8443",
		Handler: s.mux,
	}

	s.log.Info("Web Console Plugin server configured.")
	return s
}

func (s *Server) Start() {
	go func() {
		s.log.Info("Listening console plugin endpoint...")

		if err := s.svr.ListenAndServeTLS("/etc/consoleplugin/certs/"+cert.ServerCert, "/etc/consoleplugin/certs/"+cert.ServerKey); err != nil {
			s.log.Error(err, "Listening and serving console plugin endpoint failed")
			os.Exit(1)
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.svr.Shutdown(ctx)
}
