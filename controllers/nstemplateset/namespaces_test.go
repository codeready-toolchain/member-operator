package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestFindNamespace(t *testing.T) {

	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)

	namespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev",
				Labels: map[string]string{
					"toolchain.dev.openshift.com/type": "dev",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage",
				Labels: map[string]string{
					"toolchain.dev.openshift.com/type": "stage",
				},
			},
		},
	}

	t.Run("found", func(t *testing.T) {
		typeName := "dev"
		namespace, found := findNamespace(namespaces, typeName)
		require.True(t, found)
		assert.NotNil(t, namespace)
		assert.Equal(t, typeName, namespace.GetLabels()[toolchainv1alpha1.TypeLabelKey])
	})

	t.Run("not found", func(t *testing.T) {
		typeName := "other"
		_, found := findNamespace(namespaces, typeName)
		assert.False(t, found)
	})
}

func TestNextNamespaceToProvisionOrUpdate(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	nsTmplSet := newNSTmplSet("toolchain-member", "johnsmith", "basic", withNamespaces("abcde11", "dev", "stage"))
	manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)

	t.Run("return namespace whose revision is not set", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()

		delete(userNamespaces[1].Labels, toolchainv1alpha1.TemplateRefLabelKey)

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "stage", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-stage", userNS.GetName())
	})

	t.Run("return namespace whose revision is different than in tier", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels[toolchainv1alpha1.TemplateRefLabelKey] = "basic-stage-123"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "stage", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-stage", userNS.GetName())
	})

	t.Run("return namespace whose tier label is different than the tier name", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[0].Labels[toolchainv1alpha1.TierLabelKey] = "advanced"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "dev", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-dev", userNS.GetName())
	})

	t.Run("return namespace whose tier is different", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels[toolchainv1alpha1.TemplateRefLabelKey] = "outdated"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, tierTemplate)
		assert.Equal(t, "stage", tierTemplate.typeName)
		require.NotNil(t, userNS)
		assert.Equal(t, "johnsmith-stage", userNS.GetName())
	})

	t.Run("return namespace that is not part of user namespaces", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels[toolchainv1alpha1.TemplateRefLabelKey] = "basic-stage-abcde21"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "other", tierTemplate.typeName)
		assert.Nil(t, userNS)
	})

	t.Run("namespace not found", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces = append(userNamespaces, corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-other", Labels: map[string]string{
					toolchainv1alpha1.TierLabelKey:        "basic",
					toolchainv1alpha1.TypeLabelKey:        "other",
					toolchainv1alpha1.TemplateRefLabelKey: "basic-other-abcde15",
					toolchainv1alpha1.OwnerLabelKey:       "johnsmith",
					toolchainv1alpha1.SpaceLabelKey:       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})

		// when
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("error in listing roleBindings", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*rbacv1.RoleBindingList); ok {
				return fmt.Errorf("mock List error")
			}
			return fakeClient.Client.List(ctx, list, opts...)
		}
		// when
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		assert.Error(t, err, "mock List error")
		require.True(t, found)
	})

	t.Run("error in listing roles", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*rbacv1.RoleList); ok {
				return fmt.Errorf("mock List error")
			}
			return fakeClient.Client.List(ctx, list, opts...)
		}
		// when
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(logger, tierTemplates, userNamespaces)

		// then
		require.Error(t, err, "mock List error")
		require.True(t, found)
	})
}

func createUserNamespacesAndTierTemplates() ([]corev1.Namespace, []*tierTemplate) {
	userNamespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev", Labels: map[string]string{
					toolchainv1alpha1.TierLabelKey:        "basic",
					toolchainv1alpha1.TypeLabelKey:        "dev",
					toolchainv1alpha1.TemplateRefLabelKey: "basic-dev-abcde11",
					toolchainv1alpha1.OwnerLabelKey:       "johnsmith",
					toolchainv1alpha1.SpaceLabelKey:       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{
					toolchainv1alpha1.TierLabelKey:        "basic",
					toolchainv1alpha1.TypeLabelKey:        "stage",
					toolchainv1alpha1.TemplateRefLabelKey: "basic-stage-abcde21",
					toolchainv1alpha1.OwnerLabelKey:       "johnsmith",
					toolchainv1alpha1.SpaceLabelKey:       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
	tierTemplates := []*tierTemplate{
		{
			tierName:    "basic",
			typeName:    "dev",
			templateRef: "basic-dev-abcde11",
		},
		{
			tierName:    "basic",
			typeName:    "stage",
			templateRef: "basic-stage-abcde21",
		},
		{
			tierName:    "basic",
			typeName:    "other",
			templateRef: "basic-other-abcde15",
		},
	}
	return userNamespaces, tierTemplates
}

func TestNextNamespaceToDeprovision(t *testing.T) {
	// given
	userNamespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev", Labels: map[string]string{
					toolchainv1alpha1.TypeLabelKey: "dev",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{
					toolchainv1alpha1.TypeLabelKey: "stage",
				},
			},
		},
	}

	t.Run("return namespace that is not part of the tier", func(t *testing.T) {
		// given
		tierTemplates := []*tierTemplate{
			{
				templateRef: "basic-dev-abcde11",
				typeName:    "dev",
				tierName:    "basic",
			},
		}

		// when
		namespace, found := nextNamespaceToDeprovision(tierTemplates, userNamespaces)

		// then
		require.True(t, found)
		assert.Equal(t, "johnsmith-stage", namespace.Name)
	})

	t.Run("should not return any namespace", func(t *testing.T) {
		// given
		tierTemplates := []*tierTemplate{
			{
				templateRef: "basic-dev-abcde11",
				typeName:    "dev",
				tierName:    "basic",
			},
			{
				templateRef: "basic-stage-abcde21",
				typeName:    "stage",
				tierName:    "basic",
			},
		}

		// when
		namespace, found := nextNamespaceToDeprovision(tierTemplates, userNamespaces)

		// then
		assert.False(t, found)
		assert.Nil(t, namespace)
	})
}

func TestGetNamespaceName(t *testing.T) {

	// given
	namespaceName := "toolchain-member"

	t.Run("request namespace", func(t *testing.T) {
		// given
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "any-name",
				Namespace: namespaceName,
			},
		}

		// when
		nsName, err := getNamespaceName(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, namespaceName, nsName)
	})

	t.Run("watch namespace", func(t *testing.T) {
		// given
		currWatchNs := os.Getenv(commonconfig.WatchNamespaceEnvVar)
		err := os.Setenv(commonconfig.WatchNamespaceEnvVar, namespaceName)
		require.NoError(t, err)
		defer func() {
			if currWatchNs == "" {
				err := os.Unsetenv(commonconfig.WatchNamespaceEnvVar)
				require.NoError(t, err)
				return
			}
			err := os.Setenv(commonconfig.WatchNamespaceEnvVar, currWatchNs)
			require.NoError(t, err)
		}()
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "any-name",
				Namespace: "", // blank
			},
		}

		// when
		nsName, err := getNamespaceName(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, namespaceName, nsName)
	})

	t.Run("no namespace", func(t *testing.T) {
		// given
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "any-name",
				Namespace: "", // blank
			},
		}

		// when
		nsName, err := getNamespaceName(req)

		// then
		require.Error(t, err)
		assert.Equal(t, "", nsName)
	})

}

func TestEnsureNamespacesOK(t *testing.T) {

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("should create only one namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"))
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", manager.Client).
			HasNoOwnerReference().
			HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
			HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
			HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel(toolchainv1alpha1.TemplateRefLabelKey).
			HasNoLabel(toolchainv1alpha1.TierLabelKey)
		AssertThatNamespace(t, username+"-stage", manager.Client).
			DoesNotExist()
	})

	t.Run("should create the second namespace when the first one already exists", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withConditions(Provisioning()))
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS, rb)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-stage", fakeClient).
			HasNoOwnerReference().
			HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
			HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
			HasLabel(toolchainv1alpha1.TypeLabelKey, "stage").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel(toolchainv1alpha1.TemplateRefLabelKey).
			HasNoLabel(toolchainv1alpha1.TierLabelKey)

	})

	t.Run("inner resources created for existing namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withConditions(Provisioning()))
		devNS := newNamespace("", username, "dev") // NS exist but it is not complete yet
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", fakeClient).
			HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
			HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
			HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
			HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde11").
			HasLabel(toolchainv1alpha1.TierLabelKey, "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
		AssertThatNamespace(t, username+"-stage", manager.Client).
			DoesNotExist()
	})

	t.Run("ensure inner resources for stage namespace if the dev is already provisioned", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withConditions(Provisioning()))
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		codeNS := newNamespace("", username, "stage") // NS exist but it is not complete yet
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS, rb)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioning())
		for _, nsType := range []string{"stage", "dev"} {
			AssertThatNamespace(t, username+"-"+nsType, fakeClient).
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, nsType).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-"+nsType+"-abcde11").
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
		}
	})
}

func TestEnsureNamespacesFail(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	username := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("fail to create namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"))
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create namespace")
		}

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to create namespace")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("unable to create resource of kind: Namespace, version: v1: unable to create resource of kind: Namespace, version: v1: unable to create namespace"))
		AssertThatNamespace(t, username+"-dev", fakeClient).DoesNotExist()
		AssertThatNamespace(t, username+"-stage", fakeClient).DoesNotExist()
	})

	t.Run("fail to fetch namespaces", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"))
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("some error")
		}

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "some error")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvision("some error"))
		AssertThatNamespace(t, username+"-dev", fakeClient).DoesNotExist()
		AssertThatNamespace(t, username+"-stage", fakeClient).DoesNotExist()
	})

	t.Run("fail to create inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("", username, "dev") // NS exists but is missing its inner resources (since its revision is not set yet)
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS)
		fakeClient.MockCreate = func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create some object")
		}

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to create some object")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace(
				"unable to create resource of kind: RoleBinding, version: v1: unable to create resource of kind: RoleBinding, version: v1: unable to create some object"))
		AssertThatNamespace(t, username+"-dev", fakeClient).
			HasNoResource("crtadmin-pods", &rbacv1.RoleBinding{})
	})

	t.Run("fail to update status when ensuring inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
		devNS := newNamespace("advanced", username, "dev") // NS exists but is missing the resources
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update NSTmplSet")
		}

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update NSTmplSet")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions() // no condition was set (none was set)
	})

	t.Run("fail to get template for namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "fail"))
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get TierTemplates for tier 'basic'")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace(
				"unable to retrieve the TierTemplate 'basic-fail-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-fail-abcde11\" not found"))
	})

	t.Run("fail to get template for inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "fail"))
		failNS := newNamespace("basic", username, "fail") // NS exists but with an unknown type
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, failNS)

		// when
		_, err := manager.ensure(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get TierTemplates for tier 'basic'")
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace(
				"unable to retrieve the TierTemplate 'basic-fail-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-fail-abcde11\" not found"))
	})

	t.Run("fail to ensure when nextNamespaceToProvisionOrUpdate returns error", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("basic", username, "dev")
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS)
		fakeClient.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*rbacv1.RoleList); ok {
				return fmt.Errorf("mock error")
			}
			return fakeClient.Client.List(ctx, list, opts...)
		}
		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)
		//then
		require.Error(t, err)
		assert.False(t, createdOrUpdated)
	})

}

func TestDeleteNamespace(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	username := "johnsmith"
	namespaceName := "toolchain-member"
	// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "stage")
	nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "stage"), withDeletionTs())
	devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
	codeNS := newNamespace("basic", username, "stage", withTemplateRefUsingRevision("abcde11"))

	t.Run("delete user namespace", func(t *testing.T) {
		// given
		manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

		// when
		allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.False(t, allDeleted)
		// get the first namespace and check its deletion timestamp
		firstNSName := fmt.Sprintf("%s-dev", username)
		AssertThatNamespace(t, firstNSName, cl).
			DoesNotExist()
	})
	t.Run("when kube delete returns error", func(t *testing.T) {
		// given
		manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS)

		cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("client.Delete() failed")
		}
		// when
		allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)
		require.Error(t, err)
		require.False(t, allDeleted)
	})

	t.Run("with 2 user namespaces to delete", func(t *testing.T) {
		// given
		manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS)

		t.Run("delete the first namespace", func(t *testing.T) {
			// when
			allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.False(t, allDeleted)
			// get the first namespace and check its deleted
			firstNSName := fmt.Sprintf("%s-dev", username)
			AssertThatNamespace(t, firstNSName, cl).DoesNotExist()

			t.Run("delete the second namespace", func(t *testing.T) {
				// when
				allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.False(t, allDeleted)
				// get the second namespace and check its deleted
				secondNSName := fmt.Sprintf("%s-stage", username)
				AssertThatNamespace(t, secondNSName, cl).DoesNotExist()
			})

			t.Run("ensure all namespaces are deleted", func(t *testing.T) {
				allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, allDeleted)

			})
		})
	})

	t.Run("do nothing since there is no namespace to be deleted", func(t *testing.T) {
		// given
		manager, _ := prepareNamespacesManager(t, nsTmplSet)

		// when
		allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, allDeleted)
	})
	t.Run("wait for namespace to be completely deleted", func(t *testing.T) {
		// given namespace with deletion timestamp
		timeStamp := metav1.Now()
		deletedNS := codeNS.DeepCopy()
		deletedNS.DeletionTimestamp = &timeStamp
		manager, _ := prepareNamespacesManager(t, nsTmplSet, deletedNS)

		// then namespace should not be deleted
		allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)
		require.NoError(t, err)
		require.False(t, allDeleted)

		// allDeleted is still false
		allDeleted, err = manager.ensureDeleted(logger, nsTmplSet)
		require.NoError(t, err)
		require.False(t, allDeleted)
	})

	t.Run("failed to fetch namespaces", func(t *testing.T) {
		// given an NSTemplateSet resource which is being deleted and whose finalizer was not removed yet
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withDeletionTs(), withNamespaces("abcde11", "dev", "stage"))
		manager, cl := prepareNamespacesManager(t, nsTmplSet)
		cl.MockList = func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.NamespaceList); ok {
				return fmt.Errorf("mock error")
			}
			return cl.Client.List(ctx, list, opts...)
		}

		// when
		allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

		// then
		require.Error(t, err)
		assert.False(t, allDeleted)
		assert.Equal(t, "failed to list namespace with label owner 'johnsmith': mock error", err.Error())
		AssertThatNSTemplateSet(t, namespaceName, username, cl).
			HasFinalizer(). // finalizer was not added and nothing else was done
			HasConditions(UnableToTerminate("mock error"))
	})
}

func TestPromoteNamespaces(t *testing.T) {

	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	username := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("success", func(t *testing.T) {

		t.Run("upgrade dev to advanced tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			ro := newRole(devNS.Name, "exec-pods", username)
			rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, ro, rb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde11"). // upgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "advanced").
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("crtadmin-view", &rbacv1.RoleBinding{})
		})

		t.Run("upgrade dev to advanced tier by changing only the tier label", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			devNS.Labels[toolchainv1alpha1.TierLabelKey] = "base"
			ro := newRole(devNS.Name, "exec-pods", username)
			rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, ro, rb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde11"). // upgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "advanced").
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("crtadmin-view", &rbacv1.RoleBinding{})
		})

		t.Run("downgrade dev to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
			rb2 := newRoleBinding(devNS.Name, "crtadmin-view", username)
			ro := newRole(devNS.Name, "exec-pods", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, rb, rb2, ro)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde11"). // downgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasNoResource("exec-pods", &rbacv1.Role{}). // role does not exist
				HasNoResource("crtadmin-view", &rbacv1.RoleBinding{})

		})

		t.Run("delete redundant namespace while upgrading tier", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a 'dev' namespace
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("basic", username, "stage", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'stage' NS

			// when - should delete the stage namespace
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, codeNS.Name, cl).
				DoesNotExist() // namespace was deleted
			AssertThatNamespace(t, devNS.Name, cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde11"). // not upgraded yet
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic")

			t.Run("uprade dev namespace when there is no other namespace to be deleted", func(t *testing.T) {

				// when - should upgrade the -dev namespace
				updated, err := manager.ensure(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.True(t, updated)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatNamespace(t, devNS.Name, cl).
					HasNoOwnerReference().
					HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
					HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
					HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
					HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde11"). // upgraded
					HasLabel(toolchainv1alpha1.TierLabelKey, "advanced").
					HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
					HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
					HasResource("exec-pods", &rbacv1.Role{})
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("promotion to another tier fails because it cannot load current template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			// create namespace but with an unknown tier
			devNS := newNamespace("fail", username, "dev", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'fail-dev-abcde11' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"fail-dev-abcde11\" not found"))
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "fail-dev-abcde11"). // the unknown tier that caused the error
				HasLabel(toolchainv1alpha1.TierLabelKey, "fail")
		})

		t.Run("fail to delete redundant namespace while upgrading tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("basic", username, "stage", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'stage' NS
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("mock error: '%T'", obj)
			}

			// when - should delete the stage namespace
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("mock error: '*v1.Namespace'")) // failed to delete NS
			AssertThatNamespace(t, username+"-stage", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "stage").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-stage-abcde11"). // unchanged, namespace was not deleted
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic")
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde11"). // not upgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic")
		})
	})
}

func TestUpdateNamespaces(t *testing.T) {

	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	username := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("success", func(t *testing.T) {

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"))
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			ro := newRole(devNS.Name, "exec-pods", username)
			rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
			rbacRb := newRoleBinding(devNS.Name, "crtadmin-view", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, ro, rb, rbacRb)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde12"). // upgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "advanced").
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("exec-pods", &rbacv1.Role{}).
				HasNoResource("crtadmin-view", &rbacv1.RoleBinding{})
		})

		t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde12"))
			rb := newRoleBinding(devNS.Name, "crtadmin-pods", username)
			ro := newRole(devNS.Name, "exec-pods", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, rb, ro)

			// when
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde11"). // upgraded
				HasLabel(toolchainv1alpha1.TierLabelKey, "advanced").
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("exec-pods", &rbacv1.Role{}).
				HasResource("crtadmin-view", &rbacv1.RoleBinding{})
		})

		t.Run("delete redundant namespace while updating revision", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"))
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("advanced", username, "stage", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'stage' NS

			// when - should delete the stage namespace
			updated, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			assert.True(t, updated)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, codeNS.Name, cl).
				DoesNotExist() // namespace was deleted
			AssertThatNamespace(t, devNS.Name, cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "advanced-dev-abcde11").
				HasLabel(toolchainv1alpha1.TierLabelKey, "advanced")
		})

		t.Run("update to basic abcde13 tier that has a new namespace label", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde13", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel("argocd.argoproj.io/managed-by", "gitops-service-argocd")

			t.Run("next reconcile sets templateref and tier labels", func(t *testing.T) {
				// when
				_, err := manager.ensure(logger, nsTmplSet)
				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatNamespace(t, username+"-dev", cl).
					HasNoOwnerReference().
					HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
					HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
					HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
					HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
					HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde13").
					HasLabel(toolchainv1alpha1.TierLabelKey, "basic").
					HasLabel("argocd.argoproj.io/managed-by", "gitops-service-argocd")
			})
		})

		t.Run("update that has a namespace label change", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde13", "dev"))
			additionalLabels := map[string]string{"argocd.argoproj.io/managed-by": "gitops-service-argocd-original"}
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde13"), withLabels(additionalLabels))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel("argocd.argoproj.io/managed-by", "gitops-service-argocd")

			t.Run("next reconcile sets templateref and tier labels", func(t *testing.T) {
				// when
				_, err := manager.ensure(logger, nsTmplSet)
				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, cl).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatNamespace(t, username+"-dev", cl).
					HasNoOwnerReference().
					HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
					HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
					HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
					HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
					HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde13").
					HasLabel(toolchainv1alpha1.TierLabelKey, "basic").
					HasLabel("argocd.argoproj.io/managed-by", "gitops-service-argocd")
			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("update to abcde15 fails because it find the new template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde15", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UnableToProvisionNamespace(
					"unable to retrieve the TierTemplate 'basic-dev-abcde15' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-dev-abcde15\" not found"))
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde11").
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic")
		})

		t.Run("update from abcde15 fails because it find current template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde15"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'basic-dev-abcde15' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-dev-abcde15\" not found"))
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel(toolchainv1alpha1.OwnerLabelKey, username).
				HasLabel(toolchainv1alpha1.SpaceLabelKey, username).
				HasLabel(toolchainv1alpha1.TypeLabelKey, "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasLabel(toolchainv1alpha1.TemplateRefLabelKey, "basic-dev-abcde15").
				HasLabel(toolchainv1alpha1.TierLabelKey, "basic")
		})
	})
}

func TestIsUpToDateAndProvisioned(t *testing.T) {
	// given
	logger := zap.New(zap.UseDevMode(true))
	logf.SetLogger(logger)
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	nsTmplSet := newNSTmplSet("toolchain-member", "johnsmith", "basic", withNamespaces("abcde11", "dev", "stage"))
	manager, _ := prepareNamespacesManager(t, nsTmplSet)

	t.Run("namespace doesn't have the type and templateref label", func(t *testing.T) {
		//given
		devNS := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev",
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		}

		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "basic-dev-abcde11")
		require.NoError(t, err)
		// when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, &devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("namespace doesn't have the required role", func(t *testing.T) {
		//given namespace doesnt have role
		devNS := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev",
				Labels: map[string]string{
					toolchainv1alpha1.TypeLabelKey:        "dev",
					toolchainv1alpha1.TierLabelKey:        "advanced",
					toolchainv1alpha1.TemplateRefLabelKey: "advanced-dev-abcde11",
					toolchainv1alpha1.OwnerLabelKey:       "johnsmith",
					toolchainv1alpha1.SpaceLabelKey:       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		}
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", "johnsmith")
		rb2 := newRoleBinding(devNS.Name, "crtadmin-view", "johnsmith")
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, rb2)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, &devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("namespace doesn't have the required rolebinding", func(t *testing.T) {
		//given
		devNS := newNamespace("advanced", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", "johnsmith")
		role := newRole(devNS.Name, "exec-pods", "johnsmith")
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, role)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("role doesn't have the owner label", func(t *testing.T) {
		//given
		devNS := newNamespace("advanced", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", "johnsmith")
		rb2 := newRoleBinding(devNS.Name, "crtadmin-view", "johnsmith")
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: devNS.Name,
				Name:      "exec-pods",
				Labels: map[string]string{
					toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
				},
			},
		}
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, rb2, role)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("rolebinding doesn't have the owner label", func(t *testing.T) {
		//given
		devNS := newNamespace("basic", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: devNS.Name,
				Name:      "crtadmin-pods",
				Labels: map[string]string{
					toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
				},
			},
		}
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "basic-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("namespace doesn't have owner Label", func(t *testing.T) {
		//given
		devNS := newNamespace("basic", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		delete(devNS.Labels, toolchainv1alpha1.OwnerLabelKey)
		manager, _ := prepareNamespacesManager(t, nsTmplSet)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "basic-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(logger, devNS, tierTmpl)
		//then
		require.Error(t, err, "namespace doesn't have owner label")
		require.False(t, isProvisioned)

	})

	t.Run("containsRole returns error", func(t *testing.T) {
		ro := newRole("johnsmith-dev", "crtadmin-pods", "johnsmith")
		ro2 := newRole("johnsmith-dev", "crtadmin-view", "johnsmith")
		rb := newRoleBinding("johnsmith-dev", "crtadmin-pods", "johnsmith")
		roleList := []rbacv1.Role{}
		roleList = append(roleList, *ro, *ro2)
		found, err := manager.containsRole(roleList, rb, "johnsmith")
		require.Error(t, err)
		require.False(t, found)
	})

	t.Run("containsRole returns error", func(t *testing.T) {
		rb := newRoleBinding("johnsmith-dev", "crtadmin-pods", "johnsmith")
		rb2 := newRoleBinding("johnsmith-dev", "crtadmin-view", "johnsmith")
		ro := newRole("johnsmith-dev", "crtadmin-pods", "johnsmith")
		roleBindingList := []rbacv1.RoleBinding{}
		roleBindingList = append(roleBindingList, *rb, *rb2)
		found, err := manager.containsRoleBindings(roleBindingList, ro, "johnsmith")
		require.Error(t, err)
		require.False(t, found)
	})
}
