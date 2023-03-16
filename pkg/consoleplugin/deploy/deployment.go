package deploy

import (
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"

	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// certSecretName is a name of the secret
	certSecretName = "consoleplugin-certs" // nolint:gosec

	// serviceName is the name of member-operator-console-plugin service
	serviceName = "member-operator-console-plugin"
)

func ConsolePlugin(cl runtimeclient.Client, s *runtime.Scheme, namespace, image string) error {
	objs, err := getTemplateObjects(s, namespace, image)
	if err != nil {
		return errs.Wrap(err, "cannot deploy console plugin template")
	}

	applyClient := applycl.NewApplyClient(cl)
	// create all objects that are within the template, and update only when the object has changed.
	// if the object was either created or updated, then return and wait for another reconcile
	for _, obj := range objs {
		if _, err := applyClient.ApplyObject(obj); err != nil {
			return errs.Wrap(err, "cannot deploy console plugin template")
		}
	}
	return nil
}

func getTemplateObjects(s *runtime.Scheme, namespace, image string) ([]runtimeclient.Object, error) {
	deployment, err := Asset("member-operator-console-plugin.yaml")
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
		"IMAGE":     image,
	})
}
