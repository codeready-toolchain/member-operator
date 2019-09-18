package template_test

import (
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	fedcommon "sigs.k8s.io/kubefed/pkg/apis/core/common"
	fedv1b1 "sigs.k8s.io/kubefed/pkg/apis/core/v1beta1"
)

func TestGetNSTemplateTier(t *testing.T) {

	// given
	// Setup Scheme for all resources (required before adding objects in the fake client)
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)
	logf.SetLogger(zap.Logger())
	basicTier := &toolchainv1alpha1.NSTemplateTier{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "toolchain.dev.openshift.com/v1alpha1",
			Kind:       "NSTemplateTier",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "basic",
			Namespace: "toolchain-host-operator",
		},
		Spec: toolchainv1alpha1.NSTemplateTierSpec{
			Namespaces: []toolchainv1alpha1.Namespace{
				{
					Revision: "abcdef",
					Template: "{foo}",
					Type:     "ide",
				},
				{
					Revision: "1d2f3q",
					Template: "{bar}",
					Type:     "cicd",
				},
				{
					Revision: "a34r57",
					Template: "{baz}",
					Type:     "stage",
				},
			},
		},
	}
	cl := fake.NewFakeClient(basicTier)

	t.Run("success", func(t *testing.T) {
		// given
		hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
			Type:   fedcommon.ClusterReady,
			Status: apiv1.ConditionTrue,
		})
		// when
		tmpls, err := template.GetNSTemplates(hostCluster, "basic")

		// then
		require.NoError(t, err)
		require.Len(t, tmpls, 3)
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("host cluster not available", func(t *testing.T) {
			// given
			hostCluster := func() (*cluster.FedCluster, bool) {
				return nil, false
			}
			// when
			_, err := template.GetNSTemplates(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to connect to the host cluster: unknown cluster")
		})

		t.Run("host cluster not ready", func(t *testing.T) {
			// given
			hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
				Type:   fedcommon.ClusterReady,
				Status: apiv1.ConditionFalse,
			})
			// when
			_, err := template.GetNSTemplates(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the host cluster is not ready")
		})

		t.Run("unknown tier", func(t *testing.T) {
			// given
			hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
				Type:   fedcommon.ClusterReady,
				Status: apiv1.ConditionTrue,
			})
			// when
			_, err := template.GetNSTemplates(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the NSTemplateTier 'unknown' from 'Host' cluster")
		})
	})

}

func newHostCluster(cl client.Client, condition fedv1b1.ClusterCondition) cluster.GetHostClusterFunc {
	return func() (*cluster.FedCluster, bool) {
		return &cluster.FedCluster{
			Client: cl,
			ClusterStatus: &fedv1b1.KubeFedClusterStatus{
				Conditions: []fedv1b1.ClusterCondition{condition},
			},
		}, true
	}
}
