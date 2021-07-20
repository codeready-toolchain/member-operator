package deploy

import (
	"encoding/base64"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/userspodswebhook"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Webhook(cl client.Client, s *runtime.Scheme, namespace, image string) error {
	caBundle, err := cert.EnsureSecret(cl, namespace, cert.Expiration)
	if err != nil {
		return errs.Wrap(err, "cannot deploy webhook template")
	}

	toolchainObjects, err := getTemplateObjects(s, namespace, image, caBundle)
	if err != nil {
		return errs.Wrap(err, "cannot deploy webhook template")
	}

	applyClient := applycl.NewApplyClient(cl, s)
	// create all objects that are within the template, and update only when the object has changed.
	// if the object was either created or updated, then return and wait for another reconcile
	for _, toolchainObject := range toolchainObjects {
		if _, err := applyClient.ApplyObject(toolchainObject.GetClientObject()); err != nil {
			return errs.Wrap(err, "cannot deploy webhook template")
		}
	}
	return nil
}

func getTemplateObjects(s *runtime.Scheme, namespace, image string, caBundle []byte) ([]applycl.ToolchainObject, error) {
	deployment, err := userspodswebhook.Asset("member-operator-webhook.yaml")
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
		"CA_BUNDLE": base64.StdEncoding.EncodeToString(caBundle),
		"IMAGE":     image,
	})
}
