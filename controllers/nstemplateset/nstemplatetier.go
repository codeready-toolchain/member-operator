package nstemplateset

import (
	"bytes"
	"context"
	"encoding/json"
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
// Optionally, it also filters the result to return a subset of the template objects.
func (t *tierTemplate) process(scheme *runtime.Scheme, params map[string]string, filters ...template.FilterFunc) ([]runtimeclient.Object, error) {
	if t.ttr != nil {
		for i := range t.ttr.Spec.TemplateObjects {
			strTemp := string(t.ttr.Spec.TemplateObjects[i].Raw)
			obj := make([]runtimeclient.Object, len(t.ttr.Spec.TemplateObjects))
			ttrTemp, err := gotemp.New(t.ttr.Name).Parse(strTemp)
			var b bytes.Buffer

			if err != nil {
				return nil, err
			} else {
				unStruct := &unstructured.Unstructured{}
				err := ttrTemp.Execute(&b, t.ttr.Spec.Parameters)
				if err != nil {
					return nil, err
				}
				err = json.Unmarshal(b.Bytes(), unStruct)
				if err != nil {
					return nil, err
				}
				err = runtime.DefaultUnstructuredConverter.FromUnstructured(unStruct.UnstructuredContent(), obj[i])
				if err != nil {
					return nil, err
				}
				return obj, nil

			}
		}
	}

	ns, err := configuration.GetWatchNamespace()
	if err != nil {
		return nil, err
	}
	tmplProcessor := template.NewProcessor(scheme)
	params[MemberOperatorNS] = ns // add (or enforce)
	return tmplProcessor.Process(t.template.DeepCopy(), params, filters...)

}
