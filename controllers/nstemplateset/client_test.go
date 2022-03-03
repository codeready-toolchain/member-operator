package nstemplateset

import (
	"context"
	"testing"

	dbaasv1alpha1 "github.com/RHEcosystemAppEng/dbaas-operator/api/v1alpha1"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestApplyToolchainObjects(t *testing.T) {
	// given
	role := newRole("john-dev", "edit-john", "john")
	devNs := newNamespace("advanced", "john", "dev")
	dBaaSTenant := newDBaaSTenant("john-dev-tenant", "john")
	additionalLabel := map[string]string{
		"foo": "bar",
	}

	t.Run("when create two", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, objects(role, devNs), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObject(t, fakeClient, false)
	})

	t.Run("when create only one, the second is present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient, scheme.Scheme).Apply(objects(role), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, objects(role, devNs), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObject(t, fakeClient, false)
	})

	t.Run("when only DBaaSTenant is supposed to be applied but the group for DBaaS is not present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient, scheme.Scheme).Apply(objects(role, devNs), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, objects(dBaaSTenant), additionalLabel)

		// then
		require.NoError(t, err)
		assert.False(t, changed)
		assertObject(t, fakeClient, false)
	})

	t.Run("when only DBaaSTenant is supposed to be applied and the group for DBaaS is present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		apiClient.AvailableAPIGroups = append(apiClient.AvailableAPIGroups, metav1.APIGroup{Name: "dbaas.redhat.com"})
		_, err := client.NewApplyClient(fakeClient, scheme.Scheme).Apply(objects(role, devNs), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, objects(dBaaSTenant), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObject(t, fakeClient, true)
	})
}

func objects(objects ...runtimeclient.Object) []runtimeclient.Object {
	var objs []runtimeclient.Object
	for i := range objects {
		objs = append(objs, objects[i].DeepCopyObject().(runtimeclient.Object))
	}
	return objs
}

func assertObject(t *testing.T, client *test.FakeClient, expectDBaaSTenant bool) {
	AssertThatRole(t, "john-dev", "edit-john", client).
		Exists(). // created
		HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
		HasLabel(toolchainv1alpha1.OwnerLabelKey, "john").
		HasLabel("foo", "bar")
	AssertThatNamespace(t, "john-dev", client).
		HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
		HasLabel(toolchainv1alpha1.OwnerLabelKey, "john").
		HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
		HasLabel("foo", "bar")
	dBaaSTenant := &dbaasv1alpha1.DBaaSTenant{}
	if expectDBaaSTenant {
		AssertObject(t, client, "", "john-dev-tenant", dBaaSTenant, func() {
			assert.Contains(t, dBaaSTenant.Labels, toolchainv1alpha1.ProviderLabelKey)
			assert.Equal(t, toolchainv1alpha1.ProviderLabelValue, dBaaSTenant.Labels[toolchainv1alpha1.ProviderLabelKey])
			assert.Contains(t, dBaaSTenant.Labels, toolchainv1alpha1.OwnerLabelKey)
			assert.Equal(t, "john", dBaaSTenant.Labels[toolchainv1alpha1.OwnerLabelKey])
			assert.Contains(t, dBaaSTenant.Labels, "foo")
			assert.Equal(t, "bar", dBaaSTenant.Labels["foo"])
		})
	} else {
		// should not exist
		AssertObject(t, client, "john-dev", "john-dev-tenant", dBaaSTenant, nil)
	}
}

func newDBaaSTenant(name, owner string) *dbaasv1alpha1.DBaaSTenant {
	return &dbaasv1alpha1.DBaaSTenant{
		TypeMeta: metav1.TypeMeta{
			APIVersion: dbaasv1alpha1.SchemeBuilder.GroupVersion.String(),
			Kind:       "DBaaSTenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				"toolchain.dev.openshift.com/owner":    owner,
			},
			Annotations: map[string]string{
				toolchainv1alpha1.TierTemplateObjectOptionalResourceAnnotation: "true",
			},
		},
	}
}

func prepareAPIClient(t *testing.T, initObjs ...runtime.Object) (*APIClient, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()
	tierTemplates, err := prepareTemplateTiers(decoder)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, append(initObjs, tierTemplates...)...)
	resetCache()

	// objects created from OpenShift templates are `*unstructured.Unstructured`,
	// which causes troubles when calling the `List` method on the fake client,
	// so we're explicitly converting the objects during their creation and update
	fakeClient.MockCreate = func(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.CreateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		if err := test.Create(ctx, fakeClient, o, opts...); err != nil {
			return err
		}
		obj.SetGeneration(o.GetGeneration())
		return nil
	}
	fakeClient.MockUpdate = func(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.UpdateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		if err := test.Update(ctx, fakeClient, o, opts...); err != nil {
			return err
		}
		obj.SetGeneration(o.GetGeneration())
		return nil
	}
	return &APIClient{
		AllNamespacesClient: fakeClient,
		Client:              fakeClient,
		Scheme:              s,
		GetHostCluster:      NewGetHostCluster(fakeClient, true, corev1.ConditionTrue),
		AvailableAPIGroups:  newAPIGroups("quota.openshift.io", "rbac.authorization.k8s.io", "toolchain.dev.openshift.com", ""),
	}, fakeClient
}

func newAPIGroups(groups ...string) []metav1.APIGroup {
	var apiGroups []metav1.APIGroup
	for _, group := range groups {
		apiGroups = append(apiGroups, metav1.APIGroup{
			Name: group,
		})
	}
	return apiGroups
}
