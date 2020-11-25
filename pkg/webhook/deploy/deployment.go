package deploy

import (
	"context"
	"encoding/base64"

	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/cert"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/userspodswebhook"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var webhookDeploymentLog = logf.Log.WithName("webhook_deployment")

const (
	certSecretName = "webhook-certs"
)

func Webhook(cl client.Client, s *runtime.Scheme, namespace, image string) error {
	caBundle, err := ensureCertSecret(cl, namespace)
	if err != nil {
		return err
	}

	toolchainObjects, err := getTemplateObjects(s, namespace, image, caBundle)
	if err != nil {
		return err
	}

	applyClient := applycl.NewApplyClient(cl, s)
	// create all objects that are within the template, and update only when the object has changed.
	// if the object was either created or updated, then return and wait for another reconcile
	for _, toolchainObject := range toolchainObjects {
		if _, err := applyClient.CreateOrUpdateObject(toolchainObject.GetRuntimeObject(), false, nil); err != nil {
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

func ensureCertSecret(cl client.Client, namespace string) ([]byte, error) {
	certSecret := &v1.Secret{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: certSecretName}, certSecret); err != nil && !errors.IsNotFound(err) {
		return nil, err
	} else if err != nil {
		certSecret, err = cert.CreateSecret(certSecretName, namespace, "member-operator-webhook")
		if err != nil {
			return nil, err
		}
		if err := cl.Create(context.TODO(), certSecret); err != nil {
			return nil, err
		}
	}
	return certSecret.Data[cert.CACert], nil
}
