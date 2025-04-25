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
func getToolchainTierTemplateRevision(ctx context.Context, host *cluster.CachedToolchainCluster, templateRef string) (*toolchainv1alpha1.TierTemplateRevision, error) {

	if templateRef == "" {
		return nil, fmt.Errorf("templateRef is not provided - it's not possible to fetch related TierTemplateRevision resource")
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
