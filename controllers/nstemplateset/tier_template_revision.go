package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/host"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
)

// getTierTemplateRevision gets the TierTemplateRevision resource from the host cluster.
func getTierTemplateRevision(ctx context.Context, getHostClient host.ClientGetter, templateRef string) (*toolchainv1alpha1.TierTemplateRevision, error) {
	// get the host client
	hostClient, err := getHostClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to the host cluster: %w", err)
	}

	tierTemplateRevision := &toolchainv1alpha1.TierTemplateRevision{}
	err = hostClient.Get(ctx, types.NamespacedName{
		Namespace: hostClient.Namespace,
		Name:      templateRef,
	}, tierTemplateRevision)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to retrieve the TierTemplateRevision '%s' from 'Host' cluster", templateRef)
	}
	return tierTemplateRevision, nil
}
