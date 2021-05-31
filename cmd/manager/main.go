package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/controllers/idler"
	"github.com/codeready-toolchain/member-operator/controllers/memberstatus"
	"github.com/codeready-toolchain/member-operator/controllers/nstemplateset"
	"github.com/codeready-toolchain/member-operator/controllers/useraccount"
	"github.com/codeready-toolchain/member-operator/controllers/useraccountstatus"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/autoscaler"
	"github.com/codeready-toolchain/member-operator/pkg/che"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy"
	"github.com/codeready-toolchain/member-operator/version"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/controller/toolchaincluster"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	kubemetrics "github.com/operator-framework/operator-sdk/pkg/kube-metrics"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/operator-framework/operator-sdk/pkg/metrics"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

// Change below variables to serve metrics on different host or port.
var (
	metricsHost               = "0.0.0.0"
	metricsPort         int32 = 8383
	operatorMetricsPort int32 = 8686

	setupLog = ctrl.Log.WithName("setup")
	scheme   = k8sscheme.Scheme
)

func init() {
	utilruntime.Must(apis.AddToScheme(scheme))
}

func printVersion() {
	setupLog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	setupLog.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
	setupLog.Info(fmt.Sprintf("Commit: %s", version.Commit))
	setupLog.Info(fmt.Sprintf("BuildTime: %s", version.BuildTime))
}

func main() {
	// Add the zap logger flag set to the CLI. The flag set must
	// be added before calling pflag.Parse().
	pflag.CommandLine.AddFlagSet(zap.FlagSet())

	// Add flags registered by imported packages (e.g. glog and
	// controller-runtime)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	pflag.Parse()

	// Use a zap logr.Logger implementation. If none of the zap
	// flags are configured (or if the zap flag set is not being
	// used), this defaults to a production zap logger.
	//
	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(zap.Logger())

	printVersion()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	crtConfig, err := getCRTConfiguration(cfg)
	if err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}
	crtConfig.Print()

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		setupLog.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	ctx := context.TODO()
	// Become the leader before proceeding
	err = leader.Become(ctx, "member-operator-lock")
	if err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	// Set default manager options
	options := manager.Options{
		Namespace:          namespace,
		MetricsBindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort),
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(namespace, ","))
	}

	// Create a new manager to provide shared dependencies and start components
	mgr, err := manager.New(cfg, options)
	if err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	setupLog.Info("Registering Components.")

	allNamespacesClient, allNamespacesCache, err := newAllNamespacesClient(cfg)
	if err != nil {
		setupLog.Error(err, "")
		os.Exit(1)
	}

	// initialize che client
	che.InitDefaultCheClient(crtConfig, allNamespacesClient)

	// Setup all Controllers
	if err = toolchaincluster.NewReconciler(
		mgr,
		ctrl.Log.WithName("controllers").WithName("ToolchainCluster"),
		namespace,
		crtConfig.GetToolchainClusterTimeout(),
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ToolchainCluster")
		os.Exit(1)
	}
	if err := (&idler.Reconciler{
		Client:              mgr.GetClient(),
		Log:                 ctrl.Log.WithName("controllers").WithName("Idler"),
		Scheme:              mgr.GetScheme(),
		AllNamespacesClient: allNamespacesClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Idler")
		os.Exit(1)
	}
	if err = (&memberstatus.Reconciler{
		Client:              mgr.GetClient(),
		Log:                 ctrl.Log.WithName("controllers").WithName("MemberStatus"),
		Scheme:              mgr.GetScheme(),
		GetHostCluster:      cluster.GetHostCluster,
		Config:              crtConfig,
		AllNamespacesClient: allNamespacesClient,
		CheClient:           che.DefaultClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MemberStatus")
		os.Exit(1)
	}
	if err = (nstemplateset.NewReconciler(&nstemplateset.APIClient{
		Client:         mgr.GetClient(),
		Log:            ctrl.Log.WithName("controllers").WithName("NSTemplateSet"),
		Scheme:         mgr.GetScheme(),
		GetHostCluster: cluster.GetHostCluster,
	})).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NSTemplateSet")
		os.Exit(1)
	}
	if err = (&useraccount.Reconciler{
		Client:    mgr.GetClient(),
		Log:       ctrl.Log.WithName("controllers").WithName("UserAccount"),
		Scheme:    mgr.GetScheme(),
		Config:    crtConfig,
		CheClient: che.DefaultClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UserAccount")
		os.Exit(1)
	}
	if err = (&useraccountstatus.Reconciler{
		Client:         mgr.GetClient(),
		Log:            ctrl.Log.WithName("controllers").WithName("UserAccountStatus"),
		Scheme:         mgr.GetScheme(),
		GetHostCluster: cluster.GetHostCluster,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "UserAccountStatus")
		os.Exit(1)
	}

	// Add the Metrics Service
	addMetrics(ctx, cfg)

	stopChannel := signals.SetupSignalHandler()

	go func() {
		setupLog.Info("Waiting for cache to sync")
		if !mgr.GetCache().WaitForCacheSync(stopChannel) {
			setupLog.Error(fmt.Errorf("timed out waiting for main cache to sync"), "")
			os.Exit(1)
		}

		// By default the users' pods webhook will be deployed, however in some cases (eg. e2e tests) there can be multiple member operators
		// installed in the same cluster. In those cases only 1 webhook is needed because the MutatingWebhookConfiguration is a cluster-scoped resource and naming can conflict.
		if crtConfig.DoDeployWebhook() {
			setupLog.Info("(Re)Deploying users' pods webhook")
			if err := deploy.Webhook(mgr.GetClient(), mgr.GetScheme(), namespace, crtConfig.GetMemberOperatorWebhookImage()); err != nil {
				setupLog.Error(err, "cannot deploy mutating users' pods webhook")
				os.Exit(1)
			}
			setupLog.Info("(Re)Deployed users' pods webhook")
		} else {
			setupLog.Info("Skipping deployment of users' pods webhook")
		}

		if crtConfig.DoDeployAutoscalingBuffer() {
			setupLog.Info("(Re)Deploying autoscaling buffer")
			if err := autoscaler.Deploy(mgr.GetClient(), mgr.GetScheme(), namespace, crtConfig.GetAutoscalerBufferMemory(), crtConfig.GetAutoscalerBufferReplicas()); err != nil {
				setupLog.Error(err, "cannot deploy autoscaling buffer")
				os.Exit(1)
			}
			setupLog.Info("(Re)Deployed autoscaling buffer")
		} else {
			deleted, err := autoscaler.Delete(mgr.GetClient(), mgr.GetScheme(), namespace)
			if err != nil {
				setupLog.Error(err, "cannot delete previously deployed autoscaling buffer")
				os.Exit(1)
			}
			if deleted {
				setupLog.Info("Deleted previously deployed autoscaling buffer")
			} else {
				setupLog.Info("Skipping deployment of autoscaling buffer")
			}
		}

		setupLog.Info("Starting ToolchainCluster health checks.")
		toolchaincluster.StartHealthChecks(mgr, namespace, stopChannel, crtConfig.GetClusterHealthCheckPeriod())

		// create or update Member status during the operator deployment
		setupLog.Info("Creating/updating the MemberStatus resource")
		memberStatusName := configuration.MemberStatusName
		if err := memberstatus.CreateOrUpdateResources(mgr.GetClient(), mgr.GetScheme(), namespace, memberStatusName); err != nil {
			setupLog.Error(err, "cannot create/update MemberStatus resource")
			os.Exit(1)
		}
		setupLog.Info("Created/updated the MemberStatus resource")
	}()

	// Start the Cmd
	go func() {
		if err := allNamespacesCache.Start(stopChannel); err != nil {
			setupLog.Error(err, "failed to start all-namespaces cache")
			os.Exit(1)
		}
	}()
	if err := mgr.Start(stopChannel); err != nil {
		setupLog.Error(err, "Default manager exited non-zero")
		os.Exit(1)
	}

	setupLog.Info("Starting the Cmd.")
}

// newAllNamespacesClient creates a new client that watches (as opposed to the standard client) resources in all namespaces.
// This client should be used only for resources and kinds that are retrieved from other namespaces than the watched one.
// This will help keeping a reasonable memory usage for this operator since the cache won't store all other namespace scoped
// resources (secrets, etc.).
func newAllNamespacesClient(cfg *rest.Config) (client.Client, cache.Cache, error) {
	// Create the mapper provider
	mapper, err := apiutil.NewDynamicRESTMapper(cfg)
	if err != nil {
		return nil, nil, err
	}

	// Create the cache for the cached read client and registering informers
	allNamespacesCache, err := cache.New(cfg, cache.Options{Scheme: scheme, Mapper: mapper, Namespace: ""})
	if err != nil {
		return nil, nil, err
	}
	// Create the Client for Write operations.
	c, err := client.New(cfg, client.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, nil, err
	}
	// see https://github.com/kubernetes-sigs/controller-runtime/blob/release-0.6/pkg/manager/manager.go#L374-L389
	return &client.DelegatingClient{
		Reader: &client.DelegatingReader{
			CacheReader:  allNamespacesCache,
			ClientReader: c,
		},
		Writer:       c,
		StatusClient: c,
	}, allNamespacesCache, nil

}

// addMetrics will create the Services and Service Monitors to allow the operator export the metrics by using
// the Prometheus operator
func addMetrics(ctx context.Context, cfg *rest.Config) {
	// Get the namespace the operator is currently deployed in.
	operatorNs, err := k8sutil.GetOperatorNamespace()
	if err != nil {
		if errors.Is(err, k8sutil.ErrRunLocal) {
			setupLog.Info("Skipping CR metrics server creation; not running in a cluster.")
			return
		}
	}

	// Add to the below struct any other metrics ports you want to expose.
	servicePorts := []v1.ServicePort{
		{Port: metricsPort, Name: metrics.OperatorPortName, Protocol: v1.ProtocolTCP, TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: metricsPort}},
		{Port: operatorMetricsPort, Name: metrics.CRPortName, Protocol: v1.ProtocolTCP, TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: operatorMetricsPort}},
	}

	// Create Service object to expose the metrics port(s).
	service, err := metrics.CreateMetricsService(ctx, cfg, servicePorts)
	if err != nil {
		setupLog.Info("Could not create metrics Service", "error", err.Error())
	}

	// CreateServiceMonitors will automatically create the prometheus-operator ServiceMonitor resources
	// necessary to configure Prometheus to scrape metrics from this operator.
	services := []*v1.Service{service}

	// The ServiceMonitor is created in the same namespace where the operator is deployed
	_, err = metrics.CreateServiceMonitors(cfg, operatorNs, services)
	if err != nil {
		setupLog.Info("Could not create ServiceMonitor object", "error", err.Error())
		// If this operator is deployed to a cluster without the prometheus-operator running, it will return
		// ErrServiceMonitorNotPresent, which can be used to safely skip ServiceMonitor creation.
		if err == metrics.ErrServiceMonitorNotPresent {
			setupLog.Info("Install prometheus-operator in your cluster to create ServiceMonitor objects", "error", err.Error())
		}
	}
}

// serveCRMetrics gets the Operator/CustomResource GVKs and generates metrics based on those types.
// It serves those metrics on "http://metricsHost:operatorMetricsPort".
//
// Note: not used for now: by default, this function wants to use all out CRDs, but when we have a "host-only"
// cluster, this metrics server fails to load the CRDs from etcd, which causes CrashLoops.
// If we really need this metrics server, then we should taylor the list of CRDs to expose.
func serveCRMetrics(cfg *rest.Config, operatorNs string) error { // nolint:unused,deadcode
	// The function below returns a list of filtered operator/CR specific GVKs. For more control, override the GVK list below
	// with your own custom logic. Note that if you are adding third party API schemas, probably you will need to
	// customize this implementation to avoid permissions issues.
	filteredGVK, err := k8sutil.GetGVKsFromAddToScheme(toolchainv1alpha1.AddToScheme)
	if err != nil {
		return err
	}

	// The metrics will be generated from the namespaces which are returned here.
	// NOTE that passing nil or an empty list of namespaces in GenerateAndServeCRMetrics will result in an error.
	ns, err := kubemetrics.GetNamespacesForMetrics(operatorNs)
	if err != nil {
		return err
	}

	setupLog.Info("serving metrics", "GVK", filteredGVK, "namespace", ns)
	// Generate and serve custom resource specific metrics.
	return kubemetrics.GenerateAndServeCRMetrics(cfg, ns, filteredGVK, metricsHost, operatorMetricsPort)
}

// getCRTConfiguration creates the client used for configuration and
// returns the loaded crt configuration
func getCRTConfiguration(config *rest.Config) (*configuration.Config, error) {
	// create client that will be used for retrieving the member operator config maps
	cl, err := client.New(config, client.Options{})
	if err != nil {
		return nil, err
	}

	return configuration.LoadConfig(cl)
}
