package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
)

// getTierTemplateRevision gets the TierTemplateRevision resource from the host cluster.
func getToolchainTierTemplateRevision(ctx context.Context, hostClusterFunc cluster.GetHostClusterFunc, templateRef string) (*toolchainv1alpha1.TierTemplateRevision, error) {
	// retrieve the ToolchainCluster instance representing the host cluster
	host, ok := hostClusterFunc()
	if !ok {
		return nil, fmt.Errorf("unable to connect to the host cluster: unknown cluster")
	}
	if !cluster.IsReady(host.ClusterStatus) {
		return nil, fmt.Errorf("the host cluster is not ready")
	}

	tierTemplateRevision := &toolchainv1alpha1.TierTemplateRevision{}
	err := host.Client.Get(ctx, types.NamespacedName{
		Namespace: host.OperatorNamespace,
		Name:      templateRef,
	}, tierTemplateRevision)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to retrieve the TierTemplateRevision '%s' from 'Host' cluster", templateRef)
	}
	return tierTemplateRevision, nil
}
