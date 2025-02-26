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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestApplyToolchainObjects(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	ctx := log.IntoContext(context.TODO(), logger)
	log.SetLogger(logger)
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
			Annotations: map[string]string{
				"existing": "annotation",
			},
			Labels: map[string]string{
				"existing_label": "old_value",
			},
		},
	}

	t.Run("when creating two objects", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(role, devNs, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when creating only one object because the other one already exists", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(role, devNs, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when only Deployment is supposed to be applied but the apps group is not present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(optionalDeployment), additionalLabel)

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
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(optionalDeployment), additionalLabel)

		// then
		require.NoError(t, err)
		assert.False(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("when only Deployment is supposed to be applied and the apps group is present", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		apiClient.AvailableAPIGroups = append(apiClient.AvailableAPIGroups, newAPIGroup("apps", "v1"))
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(role, devNs, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(optionalDeployment), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, true)
	})

	t.Run("update existing SA labels and annotations", func(t *testing.T) {
		// given
		// let's set a secret for the existing service account in order to check if it's preserved
		expectedSecrets := []corev1.ObjectReference{
			{
				Name:      "secret",
				Namespace: sa.Namespace,
			},
		}
		sa.Secrets = expectedSecrets

		t.Run("update SA labels", func(t *testing.T) {
			// when
			// we have a new template object to be applied
			newSaObject := sa.DeepCopy()
			// we have some new labels we want to apply to the ServiceAccounts
			newlabels := map[string]string{
				"update": "me",
			}
			// let's set some new labels also on the new object from the template and see if they will be added
			newSaObject.Labels["updated"] = "template"
			// let's set some new value to an existing label
			newSaObject.Labels["existing_label"] = "new_value"
			// final labels should be merged to the additionalLabels
			expectedLabels := map[string]string{
				"update":         "me",        // new label was added
				"updated":        "template",  // new label from template was added
				"existing_label": "new_value", // existing label value was updated
				"foo":            "bar",       // existing label was preserved
			}
			originalSA := sa.DeepCopy()
			client.MergeLabels(originalSA, additionalLabel)
			apiClient, fakeClient := prepareAPIClient(t, originalSA)

			// when
			changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(newSaObject), newlabels)

			// then
			require.NoError(t, err)
			assert.True(t, changed)
			fakeClient.MockGet = nil
			actualSA := &corev1.ServiceAccount{}
			AssertObject(t, fakeClient, sa.Namespace, sa.Name, actualSA, func() {
				// check that the Secrets field is still there
				assert.Equal(t, expectedSecrets, actualSA.Secrets)
				// check that new labels were applied
				assert.Equal(t, expectedLabels, actualSA.Labels)
				// check new annotations were applied
			})
		})

		t.Run("update SA annotations", func(t *testing.T) {
			// when
			// we have a new template object to be applied
			newSaObject := sa.DeepCopy()
			// and we have some new annotations we want to apply to the ServiceAccounts
			newSaObject.Annotations["update"] = "me"
			// expected annotations map
			expectedAnnotations := map[string]string{
				"existing": "annotation", // existing annotation should stay there
				"update":   "me",         // new annotation should be added
			}
			apiClient, fakeClient := prepareAPIClient(t)
			_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(sa), additionalLabel)
			require.NoError(t, err)
			called := false
			fakeClient.MockGet = func(ctx context.Context, key runtimeclient.ObjectKey, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
				if key.Name == "appstudio-user-sa" {
					require.False(t, called, "should be called only once for SA")
					called = true
				}
				return fakeClient.Client.Get(ctx, key, obj, opts...)
			}
			changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(newSaObject), additionalLabel)

			// then
			require.NoError(t, err)
			assert.True(t, changed)
			fakeClient.MockGet = nil
			actualSA := &corev1.ServiceAccount{}
			AssertObject(t, fakeClient, sa.Namespace, sa.Name, actualSA, func() {
				// check that the Secrets field is still there
				assert.Equal(t, expectedSecrets, actualSA.Secrets)
				// check new annotations were applied
				for expectedKey, expectedValue := range expectedAnnotations {
					actualValue, found := actualSA.Annotations[expectedKey]
					assert.True(t, found)
					assert.Equal(t, expectedValue, actualValue)
				}
				// check that `last-applied` annotation is still there
				_, lastAppliedFound := actualSA.Annotations[client.LastAppliedConfigurationAnnotationKey]
				assert.True(t, lastAppliedFound)
			})
		})
	})

	t.Run("update Role", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(devNs, role, sa), additionalLabel)
		require.NoError(t, err)

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(role, sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		assertObjects(t, fakeClient, false)
	})

	t.Run("create SA when it doesn't exist yet", func(t *testing.T) {
		// given
		apiClient, fakeClient := prepareAPIClient(t)
		_, err := client.NewApplyClient(fakeClient).Apply(context.TODO(), copyObjects(devNs, role), additionalLabel)
		require.NoError(t, err)
		called := false
		fakeClient.MockGet = func(ctx context.Context, key runtimeclient.ObjectKey, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
			if key.Name == "appstudio-user-sa" {
				require.False(t, called, "should be called only once for SA")
				called = true
			}
			return fakeClient.Client.Get(ctx, key, obj, opts...)
		}

		// when
		changed, err := apiClient.ApplyToolchainObjects(ctx, copyObjects(sa), additionalLabel)

		// then
		require.NoError(t, err)
		assert.True(t, changed)
		fakeClient.MockGet = nil
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
		assert.Equal(t, map[string]string{"foo": "bar", "existing_label": "old_value"}, sa.Labels)
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

func prepareAPIClient(t *testing.T, initObjs ...runtimeclient.Object) (*APIClient, *test.FakeClient) {
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

	fakeClient.MockPatch = func(ctx context.Context, obj runtimeclient.Object, patch runtimeclient.Patch, opts ...runtimeclient.PatchOption) error {
		// fake client doesn't support SSA yet, so we have to be creative here and try to mock it out. Hopefully, SSA will be merged soon and we will
		// be able to remove this.
		//
		// NOTE: this doesn't really implement SSA in any sense. It is just here so that the existing tests pass.

		// A non-SSA patch assumes the object must already exist and should break if it doesn't. The SSA patch, on the other hand, creates the object
		// if it doesn't exist.

		if patch == runtimeclient.Apply {
			copy := obj.DeepCopyObject().(runtimeclient.Object)
			if err := fakeClient.Get(ctx, runtimeclient.ObjectKeyFromObject(copy), copy); err != nil {
				if !errors.IsNotFound(err) {
					return err
				}
				if err = fakeClient.Create(ctx, copy); err != nil {
					return err
				}
			}
			// the fake client actively complains if it sees an SSA patch...
			patch = runtimeclient.Merge
		}

		return fakeClient.Client.Patch(ctx, obj, patch, opts...)
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
