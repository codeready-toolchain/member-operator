package nstemplateset

import (
	"context"
	"fmt"
	"sync"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	templatev1 "github.com/openshift/api/template/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
)

var tierTemplatesCache = newTierTemplateCache()

// getTierTemplate retrieves the TierTemplate resource with the given name from the host cluster
// and returns an instance of the tierTemplate type for it whose template content can be parsable.
// The returned tierTemplate contains all data from TierTemplate including its name.
func getTierTemplate(hostClusterFunc cluster.GetHostClusterFunc, templateRef string) (*tierTemplate, error) {
	if templateRef == "" {
		return nil, fmt.Errorf("templateRef is not provided - it's not possible to fetch related TierTemplate resource")
	}
	if tierTmpl, ok := tierTemplatesCache.get(templateRef); ok && tierTmpl != nil {
		return tierTmpl, nil
	}
	tmpl, err := getToolchainTierTemplate(hostClusterFunc, templateRef)
	if err != nil {
		return nil, err
	}
	tierTmpl := &tierTemplate{
		templateRef: templateRef,
		tierName:    tmpl.Spec.TierName,
		typeName:    tmpl.Spec.Type,
		template:    tmpl.Spec.Template,
	}
	tierTemplatesCache.add(tierTmpl)

	return tierTmpl, nil
}

// getToolchainTierTemplate gets the TierTemplate resource from the host cluster.
func getToolchainTierTemplate(hostClusterFunc cluster.GetHostClusterFunc, templateRef string) (*toolchainv1alpha1.TierTemplate, error) {
	// retrieve the ToolchainCluster instance representing the host cluster
	host, ok := hostClusterFunc()
	if !ok {
		return nil, fmt.Errorf("unable to connect to the host cluster: unknown cluster")
	}
	if !cluster.IsReady(host.ClusterStatus) {
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
func (t *tierTemplate) process(scheme *runtime.Scheme, username string, filters ...template.FilterFunc) ([]client.ToolchainObject, error) {
	tmplProcessor := template.NewProcessor(scheme)
	params := map[string]string{"USERNAME": username}
	return tmplProcessor.Process(t.template.DeepCopy(), params, filters...)
}

type tierTemplateCache struct {
	sync.RWMutex
	// tierTemplatesByTemplateRef contains tierTemplatesByTemplateRef mapped by TemplateRef key
	tierTemplatesByTemplateRef map[string]*tierTemplate
}

func newTierTemplateCache() *tierTemplateCache {
	return &tierTemplateCache{
		tierTemplatesByTemplateRef: map[string]*tierTemplate{},
	}
}

func (c *tierTemplateCache) get(templateRef string) (*tierTemplate, bool) {
	c.RLock()
	defer c.RUnlock()
	tierTemplate, ok := c.tierTemplatesByTemplateRef[templateRef]
	return tierTemplate, ok
}

func (c *tierTemplateCache) add(tierTemplate *tierTemplate) {
	c.Lock()
	defer c.Unlock()
	c.tierTemplatesByTemplateRef[tierTemplate.templateRef] = tierTemplate
}
