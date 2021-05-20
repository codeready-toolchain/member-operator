package autoscaler

import (
	"fmt"

	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"

	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
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
		if _, err := applyClient.ApplyObject(toolchainObject.GetRuntimeObject()); err != nil {
			return errs.Wrap(err, "cannot deploy autoscaling buffer template")
		}
	}
	return nil
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
