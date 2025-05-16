package memberstatus

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	. "github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateOrUpdateResources(t *testing.T) {
	// given
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	t.Run("creation", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)

		// when
		err = CreateOrUpdateResources(context.TODO(), cl, MemberOperatorNs, MemberStatusName)

		// then
		require.NoError(t, err)

		// check that the MemberStatus exists now
		AssertThatMemberStatus(t, MemberOperatorNs, "toolchain-member-status", cl).Exists()
	})

	t.Run("should return an error if creation fails ", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)
		cl.MockPatch = func(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
			return fmt.Errorf("patch failed")
		}

		// when
		err = CreateOrUpdateResources(context.TODO(), cl, MemberOperatorNs, MemberStatusName)

		// then
		require.EqualError(t, err, "unable to patch 'toolchain.dev.openshift.com/v1alpha1, Kind=MemberStatus' called 'toolchain-member-status' in namespace 'toolchain-member-operator': patch failed")
	})
}
