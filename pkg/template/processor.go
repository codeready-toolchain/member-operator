package template

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/library-go/pkg/template/generator"
	"github.com/openshift/library-go/pkg/template/templateprocessing"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/api/errors"
	errs "github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"context"
)

type Processor struct {
	cl     client.Client
	scheme *runtime.Scheme
	log    logr.Logger
}

func NewProcessor(cl client.Client, scheme *runtime.Scheme, log logr.Logger) *Processor {
	return &Processor{cl: cl, scheme: scheme, log: log}
}

func (p Processor) ProcessAndApply(tmplContent []byte, values map[string]string) error {
	objs, err := p.Process(tmplContent, values)
	if err != nil {
		return err
	}

	return apply(p.cl, objs, p.log)
}

func apply(cl client.Client, objs []runtime.RawExtension, rq logr.Logger) error {
	for _, obj := range objs {
		if obj.Object == nil {
			rq.Info("template object is nil")
			continue
		}
		gvk := obj.Object.GetObjectKind().GroupVersionKind()
		rq.Info("processing object", "version", gvk.Version, "kind", gvk.Kind)
		if err := cl.Create(context.TODO(), obj.Object); err != nil {
			if errors.IsAlreadyExists(err) {
				// If client failed to create all resources(few created, few remaining) in the first run, then to avoid
				// resource already existing errors for later runs.(it's like oc apply, don't fail to exists)
				continue
			}
			rq.Error(err, "unable to create resource", "type", gvk.Kind)
			return errs.Wrapf(err, "unable to create resource of type %s", gvk.Kind)
		}
	}
	return nil
}

// Process processes the template with the given content
func (p Processor) Process(tmplContent []byte, values map[string]string) ([]runtime.RawExtension, error) {
	scheme := p.scheme
	codecs := serializer.NewCodecFactory(scheme)
	decode := codecs.UniversalDeserializer().Decode
	obj, _, err := decode(tmplContent, nil, nil)
	if err != nil {
		p.log.Error(err, "unable to decode template")
		return nil, err
	}
	tmpl := obj.(*templatev1.Template)
	if err := injectUserVars(tmpl, values, false); err != nil {
		p.log.Error(err, "unable to inject vars in template", "error", err.Error())
		return nil, err
	}
	tmpl, err = processTemplateLocally(tmpl, scheme)
	if err != nil {
		p.log.Error(err, "unable to process template")
		return nil, err
	}
	return tmpl.Objects, nil
}

// injectUserVars injects user specific variables into the Template
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
	if err := processor.Process(tpl); len(err) > 0 {
		return nil, err.ToAggregate()
	}
	var externalResultObj templatev1.Template
	if err := scheme.Convert(tpl, &externalResultObj, nil); err != nil {
		return nil, fmt.Errorf("unable to convert template to external template object: %v", err)
	}
	return &externalResultObj, nil
}
