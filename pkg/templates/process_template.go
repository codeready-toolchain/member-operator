package templates

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/library-go/pkg/template/generator"
	"github.com/openshift/library-go/pkg/template/templateprocessing"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

// Process processes the template with the given name
func Process(scheme *runtime.Scheme, reqLogger logr.Logger, tierName string, values map[string]string) ([]runtime.RawExtension, error) {
	tmplType, found := GetTemplate(tierName)
	if !found {
		return nil, errors.Errorf("unable to get template %q", tierName)
	}
	tmplFile := tmplType.Templates[0].TemplateFile
	tmplContent, err := GetTemplateContent(tmplFile)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to get template content")
	}
	codecs := serializer.NewCodecFactory(scheme)
	decode := codecs.UniversalDeserializer().Decode
	// TODO any use of middle return param
	obj, _, err := decode(tmplContent, nil, nil)
	if err != nil {
		log.Error(err, "unable to decode template")
		return nil, err
	}
	tmpl := obj.(*templatev1.Template)
	if err := injectUserVars(tmpl, values, false); err != nil {
		log.Error(err, "unable to inject vars in template", "error", err.Error())
		return nil, err
	}
	tmpl, err = processTemplateLocally(tmpl, scheme)
	if err != nil {
		log.Error(err, "unable to process template")
		return nil, err
	}
	return tmpl.Objects, nil
}

// injectUserVars injects user specified variables into the Template
func injectUserVars(t *templatev1.Template, values map[string]string, ignoreUnknownParameters bool) error {
	for param, val := range values {
		v := templateprocessing.GetParameterByName(t, param)
		if v != nil {
			v.Value = val
			v.Generate = ""
		} else if !ignoreUnknownParameters {
			return fmt.Errorf("unknown parameter name %q", param)
		}
	}
	return nil
}

// processTemplateLocally applies the same logic that a remote call would make but makes no
// connection to the server.
func processTemplateLocally(tpl *templatev1.Template, scheme *runtime.Scheme) (*templatev1.Template, error) {
	processor := templateprocessing.NewProcessor(map[string]generator.Generator{
		"expression": generator.NewExpressionValueGenerator(rand.New(rand.NewSource(time.Now().UnixNano()))),
	})
	if errs := processor.Process(tpl); len(errs) > 0 {
		return nil, errs[0] // TODO: use errors.NewAggregate later
	}
	var externalResultObj templatev1.Template
	if err := scheme.Convert(tpl, &externalResultObj, nil); err != nil {
		return nil, fmt.Errorf("unable to convert template to external template object: %v", err)
	}
	return &externalResultObj, nil
}
