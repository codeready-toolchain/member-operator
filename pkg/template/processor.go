package template

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"time"

	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/openshift/library-go/pkg/template/generator"
	"github.com/openshift/library-go/pkg/template/templateprocessing"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
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

// Process processes the template (ie, replaces the variables with their actual values)
func (p Processor) Process(tmplContent []byte, values map[string]string) ([]runtime.RawExtension, error) {
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
	return result.Objects, nil
}

// Apply applies the objects, ie, creates or updates them on the cluster
func (p Processor) Apply(objs []runtime.RawExtension, projectActiveTimeout time.Duration) error {
	// sorting before applying to maintain correct order
	sort.Sort(ByKind(objs))

	for _, rawObj := range objs {
		obj := rawObj.Object
		if obj == nil {
			continue
		}
		gvk := obj.GetObjectKind().GroupVersionKind()
		if err := createOrUpdateObj(p.cl, obj); err != nil {
			return errs.Wrapf(err, "unable to create resource of kind: %s, version: %s", gvk.Kind, gvk.Version)
		}
		if gvk.Group == "project.openshift.io" && gvk.Version == "v1" && gvk.Kind == "ProjectRequest" {
			var prq projectv1.ProjectRequest
			err := p.scheme.Convert(obj, &prq, nil)
			if err != nil {
				return errs.Wrapf(err, "unable to convert object of kind: %s, version: %s to a ProjectRequests", gvk.Kind, gvk.Version)
			}
			// wait until the Project exists and is ready
			_, err = p.waitUntilProjectActive(prq.GetName(), projectActiveTimeout)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (p Processor) waitUntilProjectActive(namespace string, t time.Duration) (projectv1.Project, error) {
	var prj projectv1.Project
	ticker := time.NewTicker(t / 5)
	defer ticker.Stop()
	timeout := time.After(t)
	for {
		select {
		case <-ticker.C:
			err := p.cl.Get(context.TODO(), types.NamespacedName{
				Namespace: namespace,
				Name:      "",
			}, &prj)
			// something wrong happened while checking the project
			if err != nil && !apierrors.IsNotFound(err) {
				return projectv1.Project{}, errs.Wrapf(err, "failed to check if project '%s' exists", namespace)
			}
			// project exists and is active
			if prj.Status.Phase == corev1.NamespaceActive {
				return prj, nil
			}
			// project does not exist, so let's wait
		case <-timeout:
			return projectv1.Project{}, errs.Errorf("timeout while checking if project '%s' exists", namespace)
		}
	}
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
