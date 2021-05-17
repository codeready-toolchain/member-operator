package autoscaler

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/codeready-toolchain/member-operator/pkg/controller/memberstatus"
	applycl "github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/template"

	tmplv1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Deploy(cl client.Client, s *runtime.Scheme, namespace string, bufferSizeNodeSizeRatio float64) error {
	bufferSize, err := bufferSizeGi(cl, bufferSizeNodeSizeRatio)
	if err != nil {
		return err
	}
	toolchainObjects, err := getTemplateObjects(s, namespace, bufferSize)
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

func getTemplateObjects(s *runtime.Scheme, namespace string, bufferSizeGi int64) ([]applycl.ToolchainObject, error) {
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
		"MEMORY":    fmt.Sprintf("%dGi", bufferSizeGi),
	})
}

func bufferSizeGi(cl client.Client, bufferSizeNodeSizeRatio float64) (int64, error) {
	nodes := &corev1.NodeList{}
	if err := cl.List(context.TODO(), nodes); err != nil {
		return 0, err
	}
	for _, node := range nodes.Items {
		if worker(node) {
			if memoryCapacity, found := node.Status.Allocatable["memory"]; found {
				allocatableGi := int64(memoryCapacity.Value() / (1024 * 1024 * 1024))
				bufferSizeGi := int64(math.Round(bufferSizeNodeSizeRatio * float64(allocatableGi)))
				return bufferSizeGi, nil
			}
		}
	}
	return 0, errors.New("unable to obtain allocatable memory of a worker node")
}

func worker(node corev1.Node) bool {
	if _, isInfra := node.Labels[memberstatus.LabelNodeRoleInfra]; isInfra {
		return false
	}
	if _, isWorker := node.Labels[memberstatus.LabelNodeRoleWorker]; isWorker {
		return true
	}
	return false
}
