package test

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewGetHostCluster returns cluster.GetHostClusterFunc function. The cluster.CachedToolchainCluster
// that is returned by the function then contains the given client and the given status.
// If ok == false, then the function returns nil for the cluster.
func NewGetHostCluster(cl client.Client, ok bool, status v1.ConditionStatus) cluster.GetHostClusterFunc {
	if !ok {
		return func() (*cluster.CachedToolchainCluster, bool) {
			return nil, false
		}
	}
	return func() (toolchainCluster *cluster.CachedToolchainCluster, b bool) {
		return &cluster.CachedToolchainCluster{
			Config: &cluster.Config{
				Type:              cluster.Host,
				OperatorNamespace: test.HostOperatorNs,
				OwnerClusterName:  test.MemberClusterName,
			},
			Client:            cl,
			ClusterStatus: &toolchainv1alpha1.ToolchainClusterStatus{
				Conditions: []toolchainv1alpha1.ToolchainClusterCondition{{
					Type:   toolchainv1alpha1.ToolchainClusterReady,
					Status: status,
				}},
			},
		}, true
	}

}

// NewGetHostClusterWithProbe returns a cluster.GetHostClusterFunc function which returns a cluster.CachedToolchainCluster.
// The returned cluster.CachedToolchainCluster contains the given client and the given status and lastProbeTime.
// If ok == false, then the function returns nil for the cluster.
func NewGetHostClusterWithProbe(cl client.Client, ok bool, status v1.ConditionStatus, lastProbeTime metav1.Time) cluster.GetHostClusterFunc {
	if !ok {
		return func() (*cluster.CachedToolchainCluster, bool) {
			return nil, false
		}
	}
	return func() (toolchainCluster *cluster.CachedToolchainCluster, b bool) {
		return &cluster.CachedToolchainCluster{
			Config: &cluster.Config{
				Type:              cluster.Host,
				OperatorNamespace: test.HostOperatorNs,
				OwnerClusterName:  test.MemberClusterName,
			},
			Client:            cl,
			ClusterStatus: &toolchainv1alpha1.ToolchainClusterStatus{
				Conditions: []toolchainv1alpha1.ToolchainClusterCondition{{
					Type:          toolchainv1alpha1.ToolchainClusterReady,
					Status:        status,
					LastProbeTime: lastProbeTime,
				}},
			},
		}, true
	}

}
