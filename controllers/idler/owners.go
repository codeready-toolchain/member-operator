package idler

import (
	"context"
	"errors"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	commonidler "github.com/codeready-toolchain/toolchain-common/pkg/idler"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// scaleOwnerToZero fetches the whole tree of the controller owners from the provided pod.
// If any known controller owner is found, then it's scaled down (or deleted) and its kind and name is returned.
// If the pod has been running for longer than 105% of the idler timeout, it will also idle the second known owner.
// This is a workaround for cases when the top owner controller fails to idle the workload. For example the AAP controller sometimes fails to scale down StatefulSets for postgres pods owned by the top AAP CR. Scaling down the StatefulSet (AAP -> StatefulSet -> Pods) mitigates that AAP controller bug.
// Otherwise, returns empty strings.
func (i *ownerIdler) scaleOwnerToZero(ctx context.Context, pod *corev1.Pod) (string, string, error) {
	logger := log.FromContext(ctx)
	logger.Info("Scaling owner to zero")

	owners, err := i.actions.OwnerFetcher().GetOwners(ctx, pod)
	if err != nil {
		logger.Error(err, "failed to find all owners, try to idle the workload with information that is available")
	}

	commonidler.LogOwnershipChain(logger, owners, pod)

	timeoutSeconds := getTimeout(i.idler, *pod)
	opts := commonidler.Options{TimeoutSeconds: i.idler.Spec.TimeoutSeconds}
	var topOwnerKind, topOwnerName string
	attempted := false
	var errToReturn error
	for _, ownerWithGVR := range owners {
		if util.IsBeingDeleted(ownerWithGVR.Object) {
			attempted = false
			continue
		}
		owner := ownerWithGVR.Object
		ownerKind := owner.GetObjectKind().GroupVersionKind().Kind

		err = i.actions.IdleOwner(ctx, ownerWithGVR, opts)
		if errors.Is(err, commonidler.ErrUnsupportedKind) {
			continue // Skip unknown owner types
		}

		attempted = true
		// Store the first processed owner's info and preserve its error
		if topOwnerKind == "" {
			topOwnerKind = ownerKind
			topOwnerName = owner.GetName()
			errToReturn = err
		} else {
			// if it's the second known owner being processed, then stop the loop
			errToReturn = errors.Join(errToReturn, err)
			break
		}

		// If no error occurred and the pod doesn't run for longer than 105% of the idler timeout, return immediately after the first owner was idled
		if err == nil && !time.Now().After(pod.Status.StartTime.Add(time.Duration(float64(timeoutSeconds)*1.05)*time.Second)) {
			return topOwnerKind, topOwnerName, nil
		}
		logger.Info("Scaling the first known owner down either failed or the pod has been running for longer than 105% of the idler timeout. Scaling the next known owner.")
	}
	// attempted helps us to catch the case when idling of the top-level owner (eg. VM) didn't have any effect
	// and all other known owners are already being deleted - in this case, and when it's running for more than 110%
	// of the idler timeout, we should return empty string to trigger deletion of the pod
	if !attempted && time.Now().After(pod.Status.StartTime.Add(time.Duration(float64(timeoutSeconds)*1.10)*time.Second)) {
		return "", "", errToReturn
	}

	// Return the first processed owner's info (or empty if none were processed), and the list of errors (if any happened)
	return topOwnerKind, topOwnerName, errToReturn
}
