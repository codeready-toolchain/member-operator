package main

import (
	"context"
	"flag"
	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/healthcheck"
	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin/scriptserver"
	"github.com/codeready-toolchain/member-operator/pkg/klog"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	userv1 "github.com/openshift/api/user/v1"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	klogv1 "k8s.io/klog"
	klogv2 "k8s.io/klog/v2"
	"net/http"
	"os"
	"os/signal"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"syscall"
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

	setupLog.Info("Configuring web console plugin server ...")

	runtimeScheme := runtime.NewScheme()
	cfg, err := config.GetConfig()
	if err != nil {
		setupLog.Error(err, "getting config failed")
		os.Exit(1)
	}
	err = userv1.Install(runtimeScheme)
	if err != nil {
		setupLog.Error(err, "adding user to scheme failed")
		os.Exit(1)
	}

	_, err = client.New(cfg, client.Options{
		Scheme: runtimeScheme,
	})
	if err != nil {
		setupLog.Error(err, "creating a new client failed")
		os.Exit(1)
	}

	mux := http.NewServeMux()

	s := scriptserver.NewScriptServer()

	mux.HandleFunc("/", s.HandleScriptRequest)
	mux.HandleFunc("/api/v1/status", healthcheck.HandleHealthCheck)

	consolePluginServer := &http.Server{ //nolint:gosec
		Addr:    ":8080",
		Handler: mux,
	}

	setupLog.Info("Web Console Plugin server configured.")

	go func() {
		setupLog.Info("Listening...")

		if err := consolePluginServer.ListenAndServeTLS("/etc/webhook/certs/"+cert.ServerCert, "/etc/webhook/certs/"+cert.ServerKey); err != nil {
			setupLog.Error(err, "Listening and serving TLS failed")
			os.Exit(1)
		}
	}()

	// listen to OS shutdown signal
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	setupLog.Info("Received OS shutdown signal - shutting down Web Console Plugin server gracefully...")
	if err := consolePluginServer.Shutdown(context.Background()); err != nil {
		setupLog.Error(err, "Unable to shutdown the Web Console Plugin server")
	}
}
