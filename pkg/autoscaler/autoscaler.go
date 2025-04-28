package autoscaler

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/member-operator/deploy"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type BufferConfiguration interface {
	BufferMemory() string
	BufferCPU() string
	BufferReplicas() int
}

func Deploy(ctx context.Context, cl client.Client, s *runtime.Scheme, namespace string, config BufferConfiguration) error {
	objs, err := getTemplateObjects(s, namespace, config)
	if err != nil {
		return err
	}
	logger := log.FromContext(ctx)

	applyClient := applycl.NewApplyClient(cl)
	// create all objects that are within the template, and update only when the object has changed.
	for _, obj := range objs {
		applied, err := applyClient.ApplyObject(ctx, obj)
		if err != nil {
			return errs.Wrap(err, "cannot deploy autoscaling buffer template")
		}
		if applied {
			logger.Info("Autoscaling Buffer object created or updated", "kind", obj.GetObjectKind(), "namespace", obj.GetNamespace(), "name", obj.GetName())
		} else {
			logger.Info("Autoscaling Buffer object has not changed", "kind", obj.GetObjectKind(), "namespace", obj.GetNamespace(), "name", obj.GetName())
		}
	}
	return nil
}

// Delete deletes the autoscaling buffer app if it's deployed. Does nothing if it's not.
// Returns true if the app was deleted.
func Delete(ctx context.Context, cl client.Client, s *runtime.Scheme, namespace string) (bool, error) {
	objs, err := getTemplateObjects(s, namespace, nil)
	if err != nil {
		return false, err
	}

	var deleted bool
	for _, obj := range objs {
		unst := &unstructured.Unstructured{}
		unst.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())
		if err := cl.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, unst); err != nil {
			if !errors.IsNotFound(err) { // Ignore not found
				return false, errs.Wrap(err, "cannot get autoscaling buffer object")
			}
		} else {
			if err := cl.Delete(ctx, unst); err != nil {
				return false, errs.Wrap(err, "cannot delete autoscaling buffer object")
			}
			deleted = true
		}
	}

	return deleted, nil
}

func getTemplateObjects(s *runtime.Scheme, namespace string, config BufferConfiguration) ([]client.Object, error) {
	deployment, err := deploy.AutoScalerFS.ReadFile("templates/autoscaler/member-operator-autoscaler.yaml")
	if err != nil {
		return nil, err
	}
	decoder := serializer.NewCodecFactory(s).UniversalDeserializer()
	deploymentTemplate := &tmplv1.Template{}
	if _, _, err = decoder.Decode(deployment, nil, deploymentTemplate); err != nil {
		return nil, err
	}

	memory, cpu, replicas := "0", "0", 0
	if config != nil {
		memory = config.BufferMemory()
		cpu = config.BufferCPU()
		replicas = config.BufferReplicas()
	}
	return template.NewProcessor(s).Process(deploymentTemplate, map[string]string{
		"NAMESPACE": namespace,
		"MEMORY":    memory,
		"CPU":       cpu,
		"REPLICAS":  fmt.Sprintf("%d", replicas),
	})
}
