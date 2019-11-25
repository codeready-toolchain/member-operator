package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/kubefed/pkg/controller/util"
)

// nsTemplates the templates along with their revision number for a given tier
type nsTemplates map[string]revisionedTemplate

// revisionedTemplate a template along with its revision number
type revisionedTemplate struct {
	Revision string
	Template templatev1.Template
}

// getNSTemplates gets the templates configured in the NSTemplateTier resource
// which is fetched from the host cluster.
func getNSTemplates(hostClusterFunc cluster.GetHostClusterFunc, tierName string) (nsTemplates, error) {
	// retrieve the FedCluster instance representing the host cluster
	host, ok := hostClusterFunc()
	if !ok {
		return nil, fmt.Errorf("unable to connect to the host cluster: unknown cluster")
	}
	if !util.IsClusterReady(host.ClusterStatus) {
		return nil, fmt.Errorf("the host cluster is not ready")
	}

	tier := &toolchainv1alpha1.NSTemplateTier{}
	err := host.Client.Get(context.TODO(), types.NamespacedName{
		Namespace: host.OperatorNamespace,
		Name:      tierName,
	}, tier)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to retrieve the NSTemplateTier '%s' from 'Host' cluster", tierName)
	}
	result := nsTemplates{}
	for _, ns := range tier.Spec.Namespaces {
		result[ns.Type] = revisionedTemplate{
			Revision: ns.Revision,
			Template: ns.Template,
		}
	}
	return result, nil
}
