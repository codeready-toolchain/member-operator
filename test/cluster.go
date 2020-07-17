package test

import (
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kubefed/pkg/apis/core/common"
	"sigs.k8s.io/kubefed/pkg/apis/core/v1beta1"
)

// NewGetHostCluster returns cluster.GetHostClusterFunc function. The cluster.FedCluster
// that is returned by the function then contains the given client and the given status.
// If ok == false, then the function returns nil for the cluster.
func NewGetHostCluster(cl client.Client, ok bool, status v1.ConditionStatus) cluster.GetHostClusterFunc {
	if !ok {
		return func() (*cluster.FedCluster, bool) {
			return nil, false
		}
	}
	return func() (fedCluster *cluster.FedCluster, b bool) {
		return &cluster.FedCluster{
			Client:            cl,
			Type:              cluster.Host,
			OperatorNamespace: test.HostOperatorNs,
			OwnerClusterName:  test.MemberClusterName,
			ClusterStatus: &v1beta1.KubeFedClusterStatus{
				Conditions: []v1beta1.ClusterCondition{{
					Type:   common.ClusterReady,
					Status: status,
				}},
			},
		}, true
	}

}

// NewGetHostClusterWithProbe returns a cluster.GetHostClusterFunc function which returns a cluster.FedCluster.
// The returned cluster.FedCluster contains the given client and the given status and lastProbeTime.
// If ok == false, then the function returns nil for the cluster.
func NewGetHostClusterWithProbe(cl client.Client, ok bool, status v1.ConditionStatus, lastProbeTime metav1.Time) cluster.GetHostClusterFunc {
	if !ok {
		return func() (*cluster.FedCluster, bool) {
			return nil, false
		}
	}
	return func() (fedCluster *cluster.FedCluster, b bool) {
		return &cluster.FedCluster{
			Client:            cl,
			Type:              cluster.Host,
			OperatorNamespace: test.HostOperatorNs,
			OwnerClusterName:  test.MemberClusterName,
			ClusterStatus: &v1beta1.KubeFedClusterStatus{
				Conditions: []v1beta1.ClusterCondition{{
					Type:          common.ClusterReady,
					Status:        status,
					LastProbeTime: lastProbeTime,
				}},
			},
		}, true
	}

}
