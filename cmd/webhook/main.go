package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"

	userv1 "github.com/openshift/api/user/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("setup")

func main() {
	log.Info("Configuring webhook server ...")
	runtimeScheme := runtime.NewScheme()
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "getting config failed")
		os.Exit(1)
	}
	err = userv1.Install(runtimeScheme)
	if err != nil {
		log.Error(err, "adding user to scheme failed")
		os.Exit(1)
	}
	cl, err := client.New(cfg, client.Options{
		Scheme: runtimeScheme,
	})
	if err != nil {
		log.Error(err, "creating a new client failed")
		os.Exit(1)
	}
	validator := &validatingwebhook.Validator{
		Client: cl,
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/mutate-users-pods", mutatingwebhook.HandleMutate)
	mux.HandleFunc("/validate-users-rolebindings", validator.HandleValidate)

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
