package consoleplugin

import (
	"context"
	"github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"k8s.io/utils/strings/slices"
	"net/http"
	"os"

	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/contentserver"

	"github.com/go-logr/logr"
)

const (
	ConsolePluginServerOptionNoTLS = ServerOption("notls")
)

type ServerOption string

type Server struct {
	mux     *http.ServeMux
	svr     *http.Server
	log     logr.Logger
	options []string
}

func NewConsolePluginServer(config memberoperatorconfig.WebConsolePluginConfig, log logr.Logger,
	options ...ServerOption) *Server {
	s := &Server{
		log: log,
	}

	for _, opt := range options {
		s.options = append(s.options, string(opt))
	}

	s.mux = http.NewServeMux()
	ss := contentserver.NewContentServer(config)
	s.mux.HandleFunc("/", ss.HandleContentRequest)
	s.svr = &http.Server{ //nolint:gosec
		Addr:    ":9443",
		Handler: s.mux,
	}

	s.log.Info("Web Console Plugin server configured.")
	return s
}

func (s *Server) Start() {
	go func() {
		s.log.Info("Listening console plugin endpoint...")

		if slices.Contains(s.options, string(ConsolePluginServerOptionNoTLS)) {
			if err := s.svr.ListenAndServe(); err != nil {
				s.log.Error(err, "Listening and serving console plugin endpoint failed")
				os.Exit(1)
			}
			return
		}

		if err := s.svr.ListenAndServeTLS("/etc/consoleplugin/certs/tls.crt", "/etc/consoleplugin/certs/tls.key"); err != nil {
			s.log.Error(err, "Listening and serving console plugin endpoint failed")
			os.Exit(1)
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.svr.Shutdown(ctx)
}
