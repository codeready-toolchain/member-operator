package nstemplateset

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	templatev1 "github.com/openshift/api/template/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/kubefed/pkg/controller/util"
)

// getTemplateFromHost retrieves the TierTemplate resource with the given name from the host cluster
// and returns an instance of the tierTemplate type for it whose template content can be parsable.
// The returned tierTemplate contains all data from TierTemplate including its name.
func getTemplateFromHost(templateRef string) (*tierTemplate, error) {
	if templateRef == "" {
		return nil, nil
	}
	tmpl, err := getTierTemplate(cluster.GetHostCluster, templateRef)
	if err != nil {
		return nil, err
	}
	return &tierTemplate{
		templateRef: templateRef,
		tierName:    tmpl.Spec.TierName,
		typeName:    tmpl.Spec.Type,
		template:    tmpl.Spec.Template,
	}, nil
}

// getTierTemplate gets the TierTemplate resource from the host cluster.
func getTierTemplate(hostClusterFunc cluster.GetHostClusterFunc, templateRef string) (*toolchainv1alpha1.TierTemplate, error) {
	// retrieve the FedCluster instance representing the host cluster
	host, ok := hostClusterFunc()
	if !ok {
		return nil, fmt.Errorf("unable to connect to the host cluster: unknown cluster")
	}
	if !util.IsClusterReady(host.ClusterStatus) {
		return nil, fmt.Errorf("the host cluster is not ready")
	}

	tierTemplate := &toolchainv1alpha1.TierTemplate{}
	err := host.Client.Get(context.TODO(), types.NamespacedName{
		Namespace: host.OperatorNamespace,
		Name:      templateRef,
	}, tierTemplate)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to retrieve the TierTemplate '%s' from 'Host' cluster", templateRef)
	}
	return tierTemplate, nil
}

// tierTemplate contains all data from TierTemplate including its name
type tierTemplate struct {
	templateRef string
	tierName    string
	typeName    string
	template    templatev1.Template
}

// process processes the template inside of the tierTemplate object and replaces the USERNAME variable with the given username.
// Optionally, it also filters the result to return a subset of the template objects.
func (t *tierTemplate) process(scheme *runtime.Scheme, username string, filters ...template.FilterFunc) ([]runtime.RawExtension, error) {
	tmplProcessor := template.NewProcessor(scheme)
	params := map[string]string{"USERNAME": username}
	return tmplProcessor.Process(&t.template, params, filters...)
}
