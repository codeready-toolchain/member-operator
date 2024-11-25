package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/cert"
	"github.com/codeready-toolchain/member-operator/pkg/klog"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/mutatingwebhook"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/validatingwebhook"
	"github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	klogv1 "k8s.io/klog"
	klogv2 "k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var setupLog = ctrl.Log.WithName("setup")

func main() {

	opts := zap.Options{
		Development: true,
		Encoder: zapcore.NewJSONEncoder(zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			FunctionKey:    zapcore.OmitKey,
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}),
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	// also set the client-go logger so we get the same JSON output
	klogv2.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// see https://github.com/kubernetes/klog#coexisting-with-klogv2
	// BEGIN : hack to redirect klogv1 calls to klog v2
	// Tell klog NOT to log into STDERR. Otherwise, we risk
	// certain kinds of API errors getting logged into a directory not
	// available in a `FROM scratch` Docker container, causing us to abort
	var klogv1Flags flag.FlagSet
	klogv1.InitFlags(&klogv1Flags)
	if err := klogv1Flags.Set("logtostderr", "false"); err != nil { // By default klog v1 logs to stderr, switch that off
		setupLog.Error(err, "")
		os.Exit(1)
	}
	if err := klogv1Flags.Set("stderrthreshold", "FATAL"); err != nil { // stderrthreshold defaults to ERROR, so we don't get anything in stderr
		setupLog.Error(err, "")
		os.Exit(1)
	}
	klogv1.SetOutputBySeverity("INFO", klog.Writer{}) // tell klog v1 to use the custom writer
	// END : hack to redirect klogv1 calls to klog v2

	setupLog.Info("Configuring webhook server ...")
	runtimeScheme := runtime.NewScheme()
	cfg, err := config.GetConfig()
	if err != nil {
		setupLog.Error(err, "getting config failed")
		os.Exit(1)
	}
	err = apis.AddToScheme(runtimeScheme)
	if err != nil {
		setupLog.Error(err, "adding apis to scheme failed")
		os.Exit(1)
	}
	cl, err := client.New(cfg, client.Options{
		Scheme: runtimeScheme,
	})
	if err != nil {
		setupLog.Error(err, "creating a new client failed")
		os.Exit(1)
	}

	// keep the member cache config cache up to date
	ctx := reloadMemberConfig(cl)

	rolebindingValidator := &validatingwebhook.RoleBindingRequestValidator{
		Client: cl,
	}
	spacebindingrequestValidator := &validatingwebhook.SpaceBindingRequestValidator{
		Client: cl,
	}
	sspRequestValidator := &validatingwebhook.SSPRequestValidator{
		Client: cl,
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/mutate-users-pods", mutatingwebhook.HandleMutateUserPods)
	mux.HandleFunc("/mutate-virtual-machines", mutatingwebhook.HandleMutateVirtualMachines)
	mux.HandleFunc("/validate-users-rolebindings", rolebindingValidator.HandleValidate)
	mux.HandleFunc("/validate-spacebindingrequests", spacebindingrequestValidator.HandleValidate)
	mux.HandleFunc("/validate-ssprequests", sspRequestValidator.HandleValidate) // SSP is a CNV specific resource

	webhookServer := &http.Server{ //nolint:gosec //TODO: configure ReadHeaderTimeout (gosec G112)
		Addr:    ":8443",
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"}, // disable HTTP/2 for now

			GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, err := tls.LoadX509KeyPair("/etc/webhook/certs/"+cert.ServerCert, "/etc/webhook/certs/"+cert.ServerKey)
				if err != nil {
					return nil, fmt.Errorf("could not load TLS certs: %w", err)
				}
				return &cert, err
			},
		},
	}

	setupLog.Info("Webhook server configured.")

	go func() {
		setupLog.Info("Listening...")
		if err := webhookServer.ListenAndServeTLS("", ""); err != nil {
			setupLog.Error(err, "Listening and serving TLS failed")
			os.Exit(1)
		}
	}()

	// listening OS shutdown singal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	setupLog.Info("Received OS shutdown signal - shutting down webhook server gracefully...")
	if err := webhookServer.Shutdown(ctx); err != nil {
		setupLog.Error(err, "Unable to shutdown the webhook server")
	}
}

// reloadMemberConfig start a goroutine that keeps the cache up to date with the latest member operator configuration
func reloadMemberConfig(cl client.Client) context.Context {
	ctx := ctrl.SetupSignalHandler()
	go wait.Until(func() {
		if _, _, err := configuration.LoadLatest(cl, &toolchainv1alpha1.MemberOperatorConfig{}); err != nil {
			setupLog.Error(err, "unable to load latest member config")
		}
	}, 5*time.Second, ctx.Done())
	return ctx
}
