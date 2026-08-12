package idler

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commonidler "github.com/codeready-toolchain/toolchain-common/pkg/idler"
	corev1 "k8s.io/api/core/v1"
)

type ownerIdler struct {
	idler   *toolchainv1alpha1.Idler
	actions *commonidler.Idler
}

func newOwnerIdler(idler *toolchainv1alpha1.Idler, reconciler *Reconciler) *ownerIdler {
	return &ownerIdler{
		idler: idler,
		actions: commonidler.New(
			reconciler.DiscoveryClient,
			reconciler.DynamicClient,
			reconciler.ScalesClient,
			reconciler.RestClient,
		),
	}
}

// scaleOwnerToZero idles known controller owners for the pod using the shared idler library.
// Member uses timeout-gated second-owner policy (105%/110%) and the full kind matrix (including deletes).
// When the result recommends pod deletion (empty Kind), the controller deletes the Pod.
func (i *ownerIdler) scaleOwnerToZero(ctx context.Context, pod *corev1.Pod) (string, string, error) {
	result, err := i.actions.IdleFromPod(ctx, pod, commonidler.Options{
		TimeoutSeconds:    i.idler.Spec.TimeoutSeconds,
		SecondOwnerPolicy: commonidler.SecondOwnerAfterTimeout,
		PodTimeoutSeconds: getTimeout(i.idler, *pod),
	})
	return result.Kind, result.Name, err
}
