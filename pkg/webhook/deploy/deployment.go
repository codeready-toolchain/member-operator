package deploy

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/codeready-toolchain/member-operator/pkg/cert"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy/webhooks"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// certSecretName is a name of the secret
	certSecretName = "webhook-certs" // nolint:gosec

	// serviceName is the name of webhook service
	serviceName = "member-operator-webhook"

	// WebhookDeploymentNoDeletionAnnotation is used on webhook resources that should not be deleted when the deployment of the webhook is disabled
	WebhookDeploymentNoDeletionAnnotation = "toolchain.dev.openshift.com/no-deletion"

	// WebhookDeploymentOldNameAnnotation has to old name used to deploy the resource, this is used to replace the current object with the new one
	WebhookDeploymentOldNameAnnotation = "toolchain.dev.openshift.com/old-name"
)

var log = logf.Log.WithName("webhook_deploy")

func Webhook(ctx context.Context, cl runtimeclient.Client, s *runtime.Scheme, namespace, image string) error {
	caBundle, err := cert.EnsureSecret(ctx, cl, namespace, certSecretName, serviceName, cert.Expiration)
	if err != nil {
		return errs.Wrap(err, "cannot deploy webhook template")
	}

	objs, err := GetTemplateObjects(s, namespace, image, caBundle)
	if err != nil {
		return errs.Wrap(err, "cannot deploy webhook template")
	}

	applyClient := applycl.NewApplyClient(cl)
	// create all objects that are within the template, and update only when the object has changed.
	// if the object was either created or updated, then return and wait for another reconcile
	for _, obj := range objs {
		if _, err := applyClient.ApplyObject(ctx, obj); err != nil {
			return errs.Wrap(err, "cannot deploy webhook template")
		}
	}
	return nil
}

// Delete deletes the webhook app if it's deployed. Does nothing if it's not.
// Returns true if the app was deleted.
func Delete(ctx context.Context, cl runtimeclient.Client, s *runtime.Scheme, namespace string, oldObjectOnly bool) (bool, error) {
	objs, err := GetTemplateObjects(s, namespace, "dummy-image", []byte{00000001})
	if err != nil {
		return false, err
	}

	var deleted bool
	for _, obj := range objs {
		unst := &unstructured.Unstructured{}
		unst.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())
		objName := obj.GetName()
		_, doNotDeleteFound := obj.GetAnnotations()[WebhookDeploymentNoDeletionAnnotation]
		if doNotDeleteFound {
			// this object needs to stay
			continue
		}
		// TODO --- temporary migration step to delete the objects by using the old name
		if oldObjectOnly {
			oldName, found := obj.GetAnnotations()[WebhookDeploymentOldNameAnnotation]
			if !found {
				// this object needs to stay
				continue
			}
			objName = oldName
		}
		// TODO --- end temporary migration step
		logger := logf.FromContext(ctx).WithName("webhook_deploy").WithValues("gvk", obj.GetObjectKind().GroupVersionKind(), "name", objName, "namespace", obj.GetNamespace())
		logger.Info("Searching for object to delete")
		if err := cl.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: objName}, unst); err != nil {
			if !errors.IsNotFound(err) { // Ignore not found
				return false, errs.Wrap(err, "cannot get webhook object")
			}
			continue
		}
		logger.Info("Deleting the object")
		if err := cl.Delete(ctx, unst); err != nil {
			return false, errs.Wrap(err, "cannot delete webhook object")
		}
		deleted = true
	}

	return deleted, nil
}

func GetTemplateObjects(s *runtime.Scheme, namespace, image string, caBundle []byte) ([]runtimeclient.Object, error) {
	deployment, err := webhooks.Asset("member-operator-webhook.yaml")
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
