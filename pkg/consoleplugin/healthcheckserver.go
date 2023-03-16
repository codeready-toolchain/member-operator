package consoleplugin

import (
	"context"
	"net/http"
	"os"

	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/healthcheck"
	"github.com/go-logr/logr"
)

type HealthServer Server

func NewConsolePluginHealthServer(log logr.Logger) *HealthServer {
	s := &HealthServer{
		log: log,
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/api/v1/status", healthcheck.HandleHealthCheck)
	s.svr = &http.Server{ //nolint:gosec
		Addr:    ":8080",
		Handler: s.mux,
	}

	s.log.Info("Web Console Plugin health check server configured.")
	return s
}

func (s *HealthServer) Start() {
	go func() {
		s.log.Info("Listening health status endpoint...")

		if err := s.svr.ListenAndServe(); err != nil {
			s.log.Error(err, "Listening and serving health status endpoint failed")
			os.Exit(1)
		}
	}()
}

func (s *HealthServer) Shutdown(ctx context.Context) error {
	return s.svr.Shutdown(ctx)
}
