package autoscaler

import (
	"context"
	"fmt"

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
)

func Deploy(cl client.Client, s *runtime.Scheme, namespace, requestsMemory string, replicas int) error {
	toolchainObjects, err := getTemplateObjects(s, namespace, requestsMemory, replicas)
	if err != nil {
		return err
	}

	applyClient := applycl.NewApplyClient(cl, s)
	// create all objects that are within the template, and update only when the object has changed.
	for _, toolchainObject := range toolchainObjects {
		if _, err := applyClient.ApplyObject(toolchainObject.GetClientObject()); err != nil {
			return errs.Wrap(err, "cannot deploy autoscaling buffer template")
		}
	}
	return nil
}

// Delete deletes the autoscaling buffer app if it's deployed. Does nothing if it's not.
// Returns true if the app was deleted.
func Delete(cl client.Client, s *runtime.Scheme, namespace string) (bool, error) {
	toolchainObjects, err := getTemplateObjects(s, namespace, "0", 0)
	if err != nil {
		return false, err
	}

	var deleted bool
	for _, obj := range toolchainObjects {
		unst := &unstructured.Unstructured{}
		unst.SetGroupVersionKind(obj.GetGvk())
		if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, unst); err != nil {
			if !errors.IsNotFound(err) { // Ignore not found
				return false, errs.Wrap(err, "cannot get autoscaling buffer object")
			}
		} else {
			if err := cl.Delete(context.TODO(), unst); err != nil {
				return false, errs.Wrap(err, "cannot delete autoscaling buffer object")
			}
			deleted = true
		}
	}

	return deleted, nil
}

func getTemplateObjects(s *runtime.Scheme, namespace, requestsMemory string, replicas int) ([]applycl.ToolchainObject, error) {
	deployment, err := Asset("member-operator-autoscaler.yaml")
	if err != nil {
		return nil, err
	}
	decoder := serializer.NewCodecFactory(s).UniversalDeserializer()
	deploymentTemplate := &tmplv1.Template{}
	if _, _, err = decoder.Decode(deployment, nil, deploymentTemplate); err != nil {
		return nil, err
	}

	return template.NewProcessor(s).Process(deploymentTemplate, map[string]string{
		"NAMESPACE": namespace,
		"MEMORY":    requestsMemory,
		"REPLICAS":  fmt.Sprintf("%d", replicas),
	})
}
