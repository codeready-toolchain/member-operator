package controller

import (
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/controller"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/kubefed/pkg/controller/kubefedcluster"
	"sigs.k8s.io/kubefed/pkg/controller/util"
)

func StartKubeFedClusterControllers(mgr manager.Manager, crtConfig *configuration.Config, stopChan <-chan struct{}) error {
	if err := startHealthCheckController(mgr, crtConfig, stopChan); err != nil {
		return err
	}
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		return err
	}
	if err := controller.StartCachingController(mgr, namespace, stopChan); err != nil {
		return err
	}
	return nil
}

func startHealthCheckController(mgr manager.Manager, crtConfig *configuration.Config, stopChan <-chan struct{}) error {
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		return err
	}
	controllerConfig := &util.ControllerConfig{
		KubeConfig:              mgr.GetConfig(),
		ClusterAvailableDelay:   crtConfig.GetClusterAvailableDelay(),
		ClusterUnavailableDelay: crtConfig.GetClusterUnavailableDelay(),
		KubeFedNamespaces: util.KubeFedNamespaces{
			KubeFedNamespace: namespace,
		},
	}
	clusterHealthCheckConfig := &util.ClusterHealthCheckConfig{
		Period:           crtConfig.GetClusterHealthCheckPeriod(),
		Timeout:          crtConfig.GetClusterHealthCheckTimeout(),
		FailureThreshold: crtConfig.GetClusterHealthCheckFailureThreshold(),
		SuccessThreshold: crtConfig.GetClusterHealthCheckSuccessThreshold(),
	}
	klog.InitFlags(nil)
	return kubefedcluster.StartClusterController(controllerConfig, clusterHealthCheckConfig, stopChan)
}
