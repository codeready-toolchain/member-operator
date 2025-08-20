package nstemplateset

import (
	"bytes"
	"context"
	"maps"

	"fmt"

	gotemp "text/template"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/host"
	"github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/pkg/errors"
	errs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// getTierTemplate retrieves the TierTemplateRevision resource with the given name from the host cluster,
// if not found then falls back to the current logic of retrieving the TierTemplate
// and returns an instance of the tierTemplate type for it whose template content can be parsable.
// The returned tierTemplate contains all data from TierTemplate including its name.
func getTierTemplate(ctx context.Context, getHostClient host.ClientGetter, templateRef string) (*tierTemplate, error) {
	var tierTmpl *tierTemplate
	if templateRef == "" {
		return nil, fmt.Errorf("templateRef is not provided - it's not possible to fetch related TierTemplate/TierTemplateRevision resource")
	}

	ttr, err := getTierTemplateRevision(ctx, getHostClient, templateRef)
	if err != nil {
		if errs.IsNotFound(err) {
			tmpl, err := getToolchainTierTemplate(ctx, getHostClient, templateRef)
			if err != nil {
				return nil, err
			}
			tierTmpl = &tierTemplate{
				templateRef: templateRef,
				tierName:    tmpl.Spec.TierName,
				typeName:    tmpl.Spec.Type,
				template:    tmpl.Spec.Template,
			}
		} else {
			return nil, err
		}
	} else {
		ttrTmpl, err := getToolchainTierTemplate(ctx, getHostClient, ttr.GetLabels()[toolchainv1alpha1.TemplateRefLabelKey])
		if err != nil {
			return nil, err
		}
		tierTmpl = &tierTemplate{
			templateRef: templateRef,
			tierName:    ttrTmpl.Spec.TierName,
			typeName:    ttrTmpl.Spec.Type,
			ttr:         ttr,
		}
	}

	return tierTmpl, nil
}

// getToolchainTierTemplate gets the TierTemplate resource from the host cluster.
func getToolchainTierTemplate(ctx context.Context, getHostClient host.ClientGetter, templateRef string) (*toolchainv1alpha1.TierTemplate, error) {
	// get the host client
	hostClient, err := getHostClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to the host cluster: %w", err)
	}

	tierTemplate := &toolchainv1alpha1.TierTemplate{}
	err = hostClient.Get(ctx, types.NamespacedName{
		Namespace: hostClient.Namespace,
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
	ttr         *toolchainv1alpha1.TierTemplateRevision
}

const (
	MemberOperatorNS = "MEMBER_OPERATOR_NAMESPACE"
	Username         = "USERNAME"
	SpaceName        = "SPACE_NAME"
	Namespace        = "NAMESPACE"
)

// process processes the template inside of the tierTemplate object with the given parameters.
// it first checks if tiertemplaterevision resource is present, and process its object and
// if not present then it process the openshift template(current) logic
// Optionally, it also filters the result to return a subset of the template objects.
func (t *tierTemplate) process(scheme *runtime.Scheme, params map[string]string, filters ...template.FilterFunc) ([]runtimeclient.Object, error) {
	//check if tiertemplaterevision is present then return the runtimeclient object of ttr
	if t.ttr != nil {
		return t.processGoTemplates(params, filters...)
	}
	// if ttr is not present then process the openshift template
	ns, err := configuration.GetWatchNamespace()
	if err != nil {
		return nil, err
	}
	tmplProcessor := template.NewProcessor(scheme)
	params[MemberOperatorNS] = ns // add (or enforce)
	return tmplProcessor.Process(t.template.DeepCopy(), params, filters...)

}

// convert ttr parameters to a map
func (t *tierTemplate) convertParametersToMap(runtimeParam map[string]string) map[string]string {
	staticParamMap := map[string]string{}
	for _, params := range t.ttr.Spec.Parameters {
		staticParamMap[params.Name] = params.Value
	}
	maps.Copy(staticParamMap, runtimeParam) // need to add dynamic parameters like space-name also
	return staticParamMap

}

func (t *tierTemplate) processGoTemplates(runtimeParams map[string]string, filters ...template.FilterFunc) ([]runtimeclient.Object, error) {
	paramMap := t.convertParametersToMap(runtimeParams) // go execute requires parameters in form of map

	// Determine which templates to process
	var templatesToProcess []runtime.RawExtension
	if len(filters) == 0 {
		// No filters: process all templates directly without filtering overhead
		templatesToProcess = t.ttr.Spec.TemplateObjects
	} else {
		// With filters: populate Object field directly on original templates for filtering
		for i := range t.ttr.Spec.TemplateObjects {
			// Only populate Object field if it's not already set
			if t.ttr.Spec.TemplateObjects[i].Object == nil {
				// Unmarshal the raw Go template (with template variables as literal strings)
				var unStruct unstructured.Unstructured
				if err := yaml.Unmarshal(t.ttr.Spec.TemplateObjects[i].Raw, &unStruct); err != nil {
					return nil, fmt.Errorf("failed to unmarshal raw go template for object %d in tierTemplateRevision %q: %w; raw: %q", i, t.ttr.Name, err, string(t.ttr.Spec.TemplateObjects[i].Raw))
				}

				// Populate Object field for filtering
				t.ttr.Spec.TemplateObjects[i].Object = &unStruct
			}
		}

		// Apply standard filters using the populated Object field
		templatesToProcess = template.Filter(t.ttr.Spec.TemplateObjects, filters...)
	}

	// Parse and execute the templates to process
	objList := make([]runtimeclient.Object, 0, len(templatesToProcess))

	for i, rawExt := range templatesToProcess {
		var b bytes.Buffer
		unStruct := unstructured.Unstructured{}
		strTemp := string(rawExt.Raw)

		// Parse Go template
		ttrTemp, err := gotemp.New(t.ttr.Name).Option("missingkey=error").Parse(strTemp)
		if err != nil {
			return nil, fmt.Errorf("failed to parse go template for object %d in tierTemplateRevision %q: %w; raw: %q", i, t.ttr.Name, err, strTemp)
		}

		// Execute Go template with parameters
		if err := ttrTemp.Execute(&b, paramMap); err != nil {
			return nil, fmt.Errorf("failed to execute go template for object %d in tierTemplateRevision %q: %w; raw: %q", i, t.ttr.Name, err, strTemp)
		}

		// Decode the executed template into final object
		// Use UniversalDeserializer which handles both JSON and YAML and automatically sets GVK
		decoder := scheme.Codecs.UniversalDeserializer()
		_, _, err = decoder.Decode(b.Bytes(), nil, &unStruct)
		if err != nil {
			return nil, fmt.Errorf("failed to decode executed go template for object %d in tierTemplateRevision %q: %w; raw: %q", i, t.ttr.Name, err, strTemp)
		}

		// unstructured.Unstructured already implements runtimeclient.Object
		objList = append(objList, &unStruct)
	}

	return objList, nil
}
