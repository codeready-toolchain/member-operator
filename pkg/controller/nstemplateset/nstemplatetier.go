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

const (
	// ClusterResources the key to retrieve the cluster resources template
	ClusterResources string = "clusterResources"
)

// getTemplateFromHost retrieves the NSTemplateTier resource on the host cluster
// and returns the templatev1.Template for the given typeName.
// The typeName can be a namespace type (`code`, `dev`, etc.) or `clusterResources` for
// the (optional) cluster resources
func getTemplateFromHost(tierName, typeName string) (*templatev1.Template, error) {
	if tierName == "" {
		return nil, nil
	}
	templates, err := getTemplatesFromHost(cluster.GetHostCluster, tierName)
	if err != nil {
		return nil, err
	}
	if tmpl, exists := templates[typeName]; exists {
		return &(tmpl.Template), nil
	}
	return nil, nil
}

// templates the templates along with their revision number for a given tier
type templates map[string]versionedTemplate

// versionedTemplate a template along with its revision number
type versionedTemplate struct {
	Revision string
	Template templatev1.Template
}

// getTemplatesFromHost gets the templates configured in the NSTemplateTier resource on the host cluster.
func getTemplatesFromHost(hostClusterFunc cluster.GetHostClusterFunc, tierName string) (templates, error) {
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
	result := templates{}
	for _, ns := range tier.Spec.Namespaces {
		result[ns.Type] = versionedTemplate{
			Revision: ns.Revision,
			Template: ns.Template,
		}
	}
	if tier.Spec.ClusterResources != nil {
		result[ClusterResources] = versionedTemplate{
			Revision: tier.Spec.ClusterResources.Revision,
			Template: tier.Spec.ClusterResources.Template,
		}
	}
	return result, nil
}
