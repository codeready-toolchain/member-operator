package nstemplateset

import (
	"context"
	"fmt"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/client"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestApplyToolchainObjects(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	role := newRole("john-dev", "edit-john", "john")
	devNs := newNamespace("advanced", "john", "dev")
	optionalDeployment := newOptionalDeployment("john-dev-deployment", "john")
	additionalLabel := map[string]string{
		"foo": "bar",
	}
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "appstudio-user-sa",
			Namespace: "john-dev",
		},
	}

	t.Run("when creating two objects", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(role, devNs, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when creating only one object because the other one already exists", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(role, devNs, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when only Deployment is supposed to be applied but the apps group is not present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(optionalDeployment), additionalLabel)

		// then
		require.NoError(t, err)
		assert.False(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when only Deployment is supposed to be applied, the apps group is present, but not the version", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		// the version is different
		apiClient.AvailableAPIGroups = append(apiClient.AvailableAPIGroups, newAPIGroup("apps", "v1alpha2"))
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(optionalDeployment), additionalLabel)

		// then
		require.NoError(t, err)
		assert.False(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when only Deployment is supposed to be applied and the apps group is present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		apiClient.AvailableAPIGroups = append(apiClient.AvailableAPIGroups, newAPIGroup("apps", "v1"))
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(optionalDeployment), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, true)
	})

	t.Run("patch SA when it already exists", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(devNs, role, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("update Role and don't update SA when it already exists", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(devNs, role, sa), additionalLabel)
		require.NoError(t, err)
		fakeClient.MockUpdate = func(ctx context.Context, obj runtimeclient.Object, opts ...runtimeclient.UpdateOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "ServiceAccount" {
				return fmt.Errorf("should not update")
			}
			return fakeClient.Client.Update(ctx, obj, opts...)
		}

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(role, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("create SA when it doesn't exist yet", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(copyObjects(devNs, role), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(logger, copyObjects(sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})
}

func copyObjects(objects ...runtimeclient.Object) []runtimeclient.Object {
	var objs []runtimeclient.Object
	for i := range objects {
		objs = append(objs, objects[i].DeepCopyObject().(runtimeclient.Object))
	}
	return objs
}

func assertObjects(t *testing.T, client *test.FakeClient, expectOptionalDeployment bool) {
	AssertThatRole(t, "john-dev", "edit-john", client).
		Exists(). // created
		HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
		HasLabel(toolchainv1alpha1.SpaceLabelKey, "john").
		HasLabel("foo", "bar")
	AssertThatNamespace(t, "john-dev", client).
		HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
		HasLabel(toolchainv1alpha1.SpaceLabelKey, "john").
		HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
		HasLabel("foo", "bar")
	sa := &corev1.ServiceAccount{}
	AssertObject(t, client, "john-dev", "appstudio-user-sa", sa, func() {
		assert.Equal(t, map[string]string{"foo": "bar"}, sa.Labels)
	})
	optionalDeployment := &appsv1.Deployment{}
	if expectOptionalDeployment {
		AssertObject(t, client, "", "john-dev-deployment", optionalDeployment, func() {
			assert.Contains(t, optionalDeployment.Labels, toolchainv1alpha1.ProviderLabelKey)
			assert.Equal(t, toolchainv1alpha1.ProviderLabelValue, optionalDeployment.Labels[toolchainv1alpha1.ProviderLabelKey])
			assert.Contains(t, optionalDeployment.Labels, toolchainv1alpha1.SpaceLabelKey)
			assert.Equal(t, "john", optionalDeployment.Labels[toolchainv1alpha1.SpaceLabelKey])
			assert.Contains(t, optionalDeployment.Labels, "foo")
			assert.Equal(t, "bar", optionalDeployment.Labels["foo"])
		})
	} else {
		// should not exist
		AssertObjectNotFound(t, client, "john-dev", "john-dev-deployment", optionalDeployment)
	}
}

func newOptionalDeployment(name, owner string) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
				toolchainv1alpha1.SpaceLabelKey:    owner,
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
		AvailableAPIGroups: newAPIGroups(
			newAPIGroup("quota.openshift.io", "v1"),
			newAPIGroup("rbac.authorization.k8s.io", "v1"),
			newAPIGroup("toolchain.dev.openshift.com", "v1alpha1"),
			newAPIGroup("", "v1")),
	}, fakeClient
}

func newAPIGroup(name string, version ...string) metav1.APIGroup {
	group := metav1.APIGroup{
		Name: name,
	}
	for _, version := range version {
		group.Versions = append(group.Versions, metav1.GroupVersionForDiscovery{
			GroupVersion: fmt.Sprintf("%s/%s", name, version),
			Version:      version,
		})
	}
	return group
}

func newAPIGroups(groups ...metav1.APIGroup) []metav1.APIGroup {
	return groups
}
