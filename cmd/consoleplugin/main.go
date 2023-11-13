package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/consoleplugin"
	"github.com/codeready-toolchain/member-operator/pkg/klog"
	membercfg "github.com/codeready-toolchain/toolchain-common/pkg/configuration/memberoperatorconfig"

	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	klogv1 "k8s.io/klog"
	klogv2 "k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const gracefulTimeout = time.Second * 15

var setupLog = ctrl.Log.WithName("setup")

type shutdown interface {
	Shutdown(ctx context.Context) error
}

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

	if err := toolchainv1alpha1.AddToScheme(runtimeScheme); err != nil {
		setupLog.Error(err, "adding toolchain api to scheme failed")
		os.Exit(1)
	}
	if err := corev1.AddToScheme(runtimeScheme); err != nil {
		setupLog.Error(err, "adding core api to scheme failed")
		os.Exit(1)
	}

	cl, err := client.New(cfg, client.Options{
		Scheme: runtimeScheme,
	})
	if err != nil {
		setupLog.Error(err, "creating a new client failed")
		os.Exit(1)
	}

	config, err := membercfg.GetConfiguration(cl)
	if err != nil {
		setupLog.Error(err, "Error retrieving Configuration")
		os.Exit(1)
	}

	pluginServer := startConsolePluginService(config.WebConsolePlugin())

	gracefulShutdown(gracefulTimeout, pluginServer)
}

func startConsolePluginService(config membercfg.WebConsolePluginConfig) *consoleplugin.Server {
	consolePluginServer := consoleplugin.NewConsolePluginServer(config, setupLog)
	consolePluginServer.Start()

	return consolePluginServer
}

func gracefulShutdown(timeout time.Duration, hs ...shutdown) {
	// For a channel used for notification of just one signal value, a buffer of
	// size 1 is sufficient.
	stop := make(chan os.Signal, 1)

	// We'll accept graceful shutdowns when quit via SIGINT (Ctrl+C) or SIGTERM
	// (Ctrl+/). SIGKILL, SIGQUIT will not be caught.
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	sigReceived := <-stop
	setupLog.Info("Signal received", "signal", sigReceived.String())

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	setupLog.Info("Shutdown with timeout", "timeout", timeout.String())
	for _, s := range hs {
		if err := s.Shutdown(ctx); err != nil {
			setupLog.Error(err, "Shutdown error")
		} else {
			setupLog.Info("Server stopped")
		}
	}
}
