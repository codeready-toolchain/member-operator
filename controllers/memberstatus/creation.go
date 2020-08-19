package memberstatus

import (
	"github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	commonclient "github.com/codeready-toolchain/toolchain-common/pkg/client"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateOrUpdateResources creates a memberstatus resource with the given name in the given namespace
func CreateOrUpdateResources(client client.Client, s *runtime.Scheme, namespace, memberStatusName string) error {
	memberStatus := &v1alpha1.MemberStatus{
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      memberStatusName,
		},
		Spec: v1alpha1.MemberStatusSpec{},
	}
	cl := commonclient.NewApplyClient(client, s)
	_, err := cl.CreateOrUpdateObject(memberStatus, false, nil)
	return err
}
