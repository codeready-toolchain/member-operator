package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cmd")

func main() {
	logf.SetLogger(zap.Logger())
	log.Info("Configuring webhook server ...")

	mux := http.NewServeMux()

	mux.HandleFunc("/mutate-users-pods", mutatingwebhook.HandleMutate)

	webhookServer := &http.Server{
		Addr:    ":8443",
		Handler: mux,
	}

	log.Info("Webhook server configured.")

	go func() {
		log.Info("Listening...")
		if err := webhookServer.ListenAndServeTLS("/etc/webhook/certs/"+cert.ServerCert, "/etc/webhook/certs/"+cert.ServerKey); err != nil {
			log.Error(err, "Listening and serving TLS failed")
			os.Exit(1)
		}
	}()

	// listening OS shutdown singal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	log.Info("Received OS shutdown signal - shutting down webhook server gracefully...")
	if err := webhookServer.Shutdown(context.Background()); err != nil {
		log.Error(err, "Unable to shutdown the webhook server")
	}
}
