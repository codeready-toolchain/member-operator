package nstemplateset

import (
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"
	testcommon "github.com/codeready-toolchain/toolchain-common/pkg/test"

	templatev1 "github.com/openshift/api/template/v1"
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	fedcommon "sigs.k8s.io/kubefed/pkg/apis/core/common"
	fedv1b1 "sigs.k8s.io/kubefed/pkg/apis/core/v1beta1"
)

func newTierTemplate(tier, typeName, revision string) *toolchainv1alpha1.TierTemplate {
	return &toolchainv1alpha1.TierTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      test.NewTierTemplateName(tier, typeName, revision),
			Namespace: "toolchain-host-operator",
		},
		Spec: toolchainv1alpha1.TierTemplateSpec{
			TierName: tier,
			Type:     typeName,
			Revision: revision,
			Template: templatev1.Template{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeName,
				},
			},
		},
	}
}

func TestGetTierTemplate(t *testing.T) {

	// given
	// Setup Scheme for all resources (required before adding objects in the fake client)
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)
	logf.SetLogger(zap.Logger())

	basicTierCode := newTierTemplate("basic", "code", "abcdef")
	basicTierDev := newTierTemplate("basic", "dev", "123456")
	basicTierStage := newTierTemplate("basic", "stage", "1a2b3c")
	basicTierCluster := newTierTemplate("basic", "clusterresources", "aa11bb22")

	advancedTierCode := newTierTemplate("advanced", "code", "ghijkl")
	advancedTierDev := newTierTemplate("advanced", "dev", "789012")
	advancedTierStage := newTierTemplate("advanced", "stage", "4d5e6f")

	other := newTierTemplate("other", "other", "other")
	other.Namespace = "other"

	cl := testcommon.NewFakeClient(t, basicTierCode, basicTierDev, basicTierStage, basicTierCluster, advancedTierCode, advancedTierDev, advancedTierStage, other)

	t.Run("return code for basic tier", func(t *testing.T) {
		// given
		hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
			Type:   fedcommon.ClusterReady,
			Status: apiv1.ConditionTrue,
		})
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "basic-code-abcdef")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCode, tierTmpl)
	})

	t.Run("return dev for advanced tier", func(t *testing.T) {
		// given
		hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
			Type:   fedcommon.ClusterReady,
			Status: apiv1.ConditionTrue,
		})
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "advanced-dev-789012")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, advancedTierDev, tierTmpl)
	})

	t.Run("return cluster type for basic tier", func(t *testing.T) {
		// given
		hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
			Type:   fedcommon.ClusterReady,
			Status: apiv1.ConditionTrue,
		})
		// when
		tierTmpl, err := getTierTemplate(hostCluster, "basic-clusterresources-aa11bb22")

		// then
		require.NoError(t, err)
		assertThatTierTemplateIsSameAs(t, basicTierCluster, tierTmpl)
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("host cluster not available", func(t *testing.T) {
			// given
			hostCluster := func() (*cluster.FedCluster, bool) {
				return nil, false
			}
			// when
			_, err := getTierTemplate(hostCluster, "unknown")
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
			_, err := getTierTemplate(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the host cluster is not ready")
		})

		t.Run("unknown templateRef", func(t *testing.T) {
			// given
			hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
				Type:   fedcommon.ClusterReady,
				Status: apiv1.ConditionTrue,
			})
			// when
			_, err := getTierTemplate(hostCluster, "unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'unknown' from 'Host' cluster")
		})

		t.Run("tier in another namespace", func(t *testing.T) {
			// given
			hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
				Type:   fedcommon.ClusterReady,
				Status: apiv1.ConditionTrue,
			})
			// when
			_, err := getTierTemplate(hostCluster, "other-other-other")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the TierTemplate 'other-other-other' from 'Host' cluster")
		})

		t.Run("tier in another namespace", func(t *testing.T) {
			// given
			hostCluster := newHostCluster(cl, fedv1b1.ClusterCondition{
				Type:   fedcommon.ClusterReady,
				Status: apiv1.ConditionTrue,
			})
			// when
			_, err := getTierTemplate(hostCluster, "")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "templateRef is not provided - it's not possible to fetch related TierTemplate resource")
		})
	})
}

func newHostCluster(cl client.Client, condition fedv1b1.ClusterCondition) cluster.GetHostClusterFunc {
	return func() (*cluster.FedCluster, bool) {
		return &cluster.FedCluster{
			OperatorNamespace: "toolchain-host-operator",
			Client:            cl,
			ClusterStatus: &fedv1b1.KubeFedClusterStatus{
				Conditions: []fedv1b1.ClusterCondition{condition},
			},
		}, true
	}
}

func assertThatTierTemplateIsSameAs(t *testing.T, expected *toolchainv1alpha1.TierTemplate, actual *tierTemplate) {
	assert.Equal(t, expected.Spec.Type, actual.typeName)
	assert.Equal(t, expected.Spec.Template, actual.template)
	assert.Equal(t, expected.Name, actual.templateRef)
	assert.Equal(t, expected.Spec.TierName, actual.tierName)
}
