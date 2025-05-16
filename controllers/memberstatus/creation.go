package memberstatus

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/constants"
	commonclient "github.com/codeready-toolchain/toolchain-common/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateOrUpdateResources creates a memberstatus resource with the given name in the given namespace
func CreateOrUpdateResources(ctx context.Context, client client.Client, namespace, memberStatusName string) error {
	memberStatus := &toolchainv1alpha1.MemberStatus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      memberStatusName,
		},
		Spec: toolchainv1alpha1.MemberStatusSpec{},
	}
	cl := commonclient.NewSSAApplyClient(client, constants.MemberOperatorFieldManager)
	return cl.ApplyObject(ctx, memberStatus)
}
