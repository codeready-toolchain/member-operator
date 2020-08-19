package memberstatus

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	. "github.com/codeready-toolchain/member-operator/test"
	. "github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
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
		err = CreateOrUpdateResources(cl, s, MemberOperatorNs, configuration.DefaultMemberStatusName)

		// then
		require.NoError(t, err)

		// check that the MemberStatus exists now
		AssertThatMemberStatus(t, MemberOperatorNs, "toolchain-member-status", cl).Exists()
	})

	t.Run("should return an error if creation fails ", func(t *testing.T) {
		// given
		cl := NewFakeClient(t)
		cl.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return fmt.Errorf("creation failed")
		}

		// when
		err = CreateOrUpdateResources(cl, s, MemberOperatorNs, configuration.DefaultMemberStatusName)

		// then
		require.Error(t, err)
		require.Equal(t, err.Error(), "unable to create resource of kind: , version: : creation failed")
	})
}
