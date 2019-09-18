package template

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
)

// NSTemplates the templates along with their revision number for a given tier
type NSTemplates map[string]RevisionedTemplate

// RevisionedTemplate a template along with its revision number
type RevisionedTemplate struct {
	Revision string
	Template string
}

// GetNSTemplates gets the templates configured in the NSTemplateTier resource
// which is fetched from the host cluster.
func GetNSTemplates(hostClusterFunc cluster.GetHostClusterFunc, tierName string) (NSTemplates, error) {
	// retrieve the FedCluster instance representing the host cluster
	host, ok := hostClusterFunc()
	if !ok {
		return nil, fmt.Errorf("unable to connect to the Host cluster: unknown cluster")
	}
	tier := &toolchainv1alpha1.NSTemplateTier{}
	err := host.Client.Get(context.TODO(), types.NamespacedName{
		Namespace: host.OperatorNamespace,
		Name:      tierName,
	}, tier)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to retrieve the NSTemplateTier '%s' from 'Host' cluster", tierName)
	}
	result := NSTemplates{}
	for _, ns := range tier.Spec.Namespaces {
		result[ns.Type] = RevisionedTemplate{
			Revision: ns.Revision,
			Template: ns.Template,
		}
	}
	return result, nil
}
