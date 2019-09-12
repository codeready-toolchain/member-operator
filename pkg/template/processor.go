package template

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/library-go/pkg/template/generator"
	"github.com/openshift/library-go/pkg/template/templateprocessing"
	errs "github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Processor the tool that will process and apply a template with variables
type Processor struct {
	cl           client.Client
	scheme       *runtime.Scheme
	codecFactory serializer.CodecFactory
}

// NewProcessor returns a new Processor
func NewProcessor(cl client.Client, scheme *runtime.Scheme) Processor {
	return Processor{cl: cl, scheme: scheme, codecFactory: serializer.NewCodecFactory(scheme)}
}

// Process processes the template (ie, replaces the variables with their actual values) and optionally filters the result
// to return a subset of the template objects
func (p Processor) Process(tmplContent []byte, values map[string]string, filters ...FilterFunc) ([]runtime.RawExtension, error) {
	obj, _, err := p.codecFactory.UniversalDeserializer().Decode(tmplContent, nil, nil)
	if err != nil {
		return nil, errs.Wrapf(err, "unable to decode template")
	}
	tmpl, ok := obj.(*templatev1.Template)
	if !ok {
		return nil, fmt.Errorf("unable to convert object type %T to Template, must be a v1.Template", obj)
	}
	// inject variables in the twmplate
	for param, val := range values {
		v := templateprocessing.GetParameterByName(tmpl, param)
		if v != nil {
			v.Value = val
			v.Generate = ""
		}
	}
	// convert the template into a set of objects
	tmplProcessor := templateprocessing.NewProcessor(map[string]generator.Generator{
		"expression": generator.NewExpressionValueGenerator(rand.New(rand.NewSource(time.Now().UnixNano()))),
	})
	if err := tmplProcessor.Process(tmpl); len(err) > 0 {
		return nil, errs.Wrap(err.ToAggregate(), "unable to process template")
	}
	var result templatev1.Template
	if err := p.scheme.Convert(tmpl, &result, nil); err != nil {
		return nil, errs.Wrap(err, "failed to convert template to external template object")
	}
	return Filter(result.Objects, filters...), nil
}

// Apply applies the objects, ie, creates or updates them on the cluster
func (p Processor) Apply(objs []runtime.RawExtension) error {
	for _, rawObj := range objs {
		obj := rawObj.Object
		if obj == nil {
			continue
		}
		gvk := obj.GetObjectKind().GroupVersionKind()
		if err := createOrUpdateObj(p.cl, obj); err != nil {
			return errs.Wrapf(err, "unable to create resource of kind: %s, version: %s", gvk.Kind, gvk.Version)
		}
	}
	return nil
}

func createOrUpdateObj(cl client.Client, obj runtime.Object) error {
	if err := cl.Create(context.TODO(), obj); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return errs.Wrapf(err, "failed to create object %v", obj)
		}

		if err = cl.Update(context.TODO(), obj); err != nil {
			return errs.Wrapf(err, "failed to update object %v", obj)
		}
		return nil
	}
	return nil
}
