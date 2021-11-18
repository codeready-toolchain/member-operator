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
	namespaces := []corev1.Namespace{
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-dev", Labels: map[string]string{
			"toolchain.dev.openshift.com/type": "dev",
		}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-code", Labels: map[string]string{
			"toolchain.dev.openshift.com/type": "code",
		}}},
	}

	t.Run("found", func(t *testing.T) {
		typeName := "dev"
		namespace, found := findNamespace(namespaces, typeName)
		assert.True(t, found)
		assert.NotNil(t, namespace)
		assert.Equal(t, typeName, namespace.GetLabels()["toolchain.dev.openshift.com/type"])
	})

	t.Run("not found", func(t *testing.T) {
		typeName := "stage"
		_, found := findNamespace(namespaces, typeName)
		assert.False(t, found)
	})
}

func TestNextNamespaceToProvisionOrUpdate(t *testing.T) {
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	nsTmplSet := newNSTmplSet("toolchain-member", "johnsmith", "basic", withNamespaces("abcde11", "dev", "code"))
	manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)

	t.Run("return namespace whose revision is not set", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()

		delete(userNamespaces[1].Labels, "toolchain.dev.openshift.com/templateref")

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "code", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace whose revision is different than in tier", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels["toolchain.dev.openshift.com/templateref"] = "basic-code-123"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "code", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace whose tier label is different than the tier name", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[0].Labels["toolchain.dev.openshift.com/tier"] = "advanced"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "dev", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-dev", userNS.GetName())
	})

	t.Run("return namespace whose tier is different", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels["toolchain.dev.openshift.com/templateref"] = "advanced-code-abcde21"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "code", tierTemplate.typeName)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace that is not part of user namespaces", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels["toolchain.dev.openshift.com/templateref"] = "basic-code-abcde21"

		// when
		tierTemplate, userNS, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "stage", tierTemplate.typeName)
		assert.Nil(t, userNS)
	})

	t.Run("namespace not found", func(t *testing.T) {
		// given
		userNamespaces, tierTemplates := createUserNamespacesAndTierTemplates()
		userNamespaces[1].Labels["toolchain.dev.openshift.com/templateref"] = "basic-code-abcde21"
		userNamespaces = append(userNamespaces, corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{
					"toolchain.dev.openshift.com/type":        "stage",
					"toolchain.dev.openshift.com/templateref": "basic-stage-abcde13",
					"toolchain.dev.openshift.com/tier":        "basic",
					"toolchain.dev.openshift.com/owner":       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})

		// when
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.NoError(t, err)
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
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

		// then
		assert.Error(t, err, "mock List error")
		assert.True(t, found)
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
		_, _, found, err := manager.nextNamespaceToProvisionOrUpdate(tierTemplates, userNamespaces)

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
					"toolchain.dev.openshift.com/type":        "dev",
					"toolchain.dev.openshift.com/templateref": "basic-dev-abcde11",
					"toolchain.dev.openshift.com/tier":        "basic",
					"toolchain.dev.openshift.com/owner":       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-code", Labels: map[string]string{
					"toolchain.dev.openshift.com/type":        "code",
					"toolchain.dev.openshift.com/tier":        "basic",
					"toolchain.dev.openshift.com/templateref": "basic-code-abcde21",
					"toolchain.dev.openshift.com/owner":       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
	tierTemplates := []*tierTemplate{
		{
			templateRef: "basic-dev-abcde11",
			typeName:    "dev",
			tierName:    "basic",
		},
		{
			templateRef: "basic-code-abcde21",
			typeName:    "code",
			tierName:    "basic",
		},
		{
			templateRef: "basic-stage-abcde13",
			typeName:    "stage",
			tierName:    "basic",
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
					"toolchain.dev.openshift.com/type": "dev",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-code", Labels: map[string]string{
					"toolchain.dev.openshift.com/type": "code",
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
		assert.True(t, found)
		assert.Equal(t, "johnsmith-code", namespace.Name)
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
				templateRef: "basic-code-abcde21",
				typeName:    "code",
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

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("should create only one namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "code").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", manager.Client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel("toolchain.dev.openshift.com/templateref").
			HasNoLabel("toolchain.dev.openshift.com/tier")
		AssertThatNamespace(t, username+"-code", manager.Client).
			DoesNotExist()
	})

	t.Run("should create the second namespace when the first one already exists", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(Provisioning()))
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "user-edit", username)
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS, rb)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "code").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-code", fakeClient).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "code").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel("toolchain.dev.openshift.com/templateref").
			HasNoLabel("toolchain.dev.openshift.com/tier")

	})

	t.Run("inner resources created for existing namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(Provisioning()))
		devNS := newNamespace("", username, "dev") // NS exist but it is not complete yet
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "code").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", fakeClient).
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev").
			HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
			HasLabel("toolchain.dev.openshift.com/tier", "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasResource("user-edit", &rbacv1.RoleBinding{})
		AssertThatNamespace(t, username+"-code", manager.Client).
			DoesNotExist()
	})

	t.Run("ensure inner resources for code namespace if the dev is already provisioned", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withConditions(Provisioning()))
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		codeNS := newNamespace("", username, "code") // NS exist but it is not complete yet
		rb := newRoleBinding(devNS.Name, "user-edit", username)
		manager, fakeClient := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS, rb)

		// when
		createdOrUpdated, err := manager.ensure(logger, nsTmplSet)

		// then
		require.NoError(t, err)
		assert.True(t, createdOrUpdated)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "code").
			HasConditions(Provisioning())
		for _, nsType := range []string{"code", "dev"} {
			AssertThatNamespace(t, username+"-"+nsType, fakeClient).
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", nsType).
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-"+nsType+"-abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("user-edit", &rbacv1.RoleBinding{})
		}
	})
}

func TestEnsureNamespacesFail(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("fail to create namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
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
		AssertThatNamespace(t, username+"-code", fakeClient).DoesNotExist()
	})

	t.Run("fail to fetch namespaces", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
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
		AssertThatNamespace(t, username+"-code", fakeClient).DoesNotExist()
	})

	t.Run("fail to create inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
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
			HasNoResource("user-edit", &rbacv1.RoleBinding{})
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
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
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
	username := "johnsmith"
	namespaceName := "toolchain-member"
	// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
	nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"), withDeletionTs())
	devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
	codeNS := newNamespace("basic", username, "code", withTemplateRefUsingRevision("abcde11"))

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
			firstNSName := fmt.Sprintf("%s-code", username)
			AssertThatNamespace(t, firstNSName, cl).DoesNotExist()

			t.Run("delete the second namespace", func(t *testing.T) {
				// when
				allDeleted, err := manager.ensureDeleted(logger, nsTmplSet)

				// then
				require.NoError(t, err)
				assert.False(t, allDeleted)
				// get the second namespace and check its deleted
				secondNSName := fmt.Sprintf("%s-dev", username)
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
		manager, cl := prepareNamespacesManager(t, nsTmplSet, codeNS)
		timeStamp := metav1.Now()
		codeNS.DeletionTimestamp = &timeStamp
		err := cl.Client.Update(context.TODO(), codeNS)
		require.NoError(t, err)

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
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withDeletionTs(), withNamespaces("abcde11", "dev", "code"))
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

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
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
			ro := newRole(devNS.Name, "rbac-edit", username)
			rb := newRoleBinding(devNS.Name, "user-edit", username)
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // upgraded
				HasLabel("toolchain.dev.openshift.com/tier", "advanced").
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasResource("user-edit", &rbacv1.RoleBinding{}).
				HasResource("user-rbac-edit", &rbacv1.RoleBinding{})
		})

		t.Run("upgrade dev to advanced tier by changing only the tier label", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			devNS.Labels["toolchain.dev.openshift.com/tier"] = "base"
			ro := newRole(devNS.Name, "rbac-edit", username)
			rb := newRoleBinding(devNS.Name, "user-edit", username)
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // upgraded
				HasLabel("toolchain.dev.openshift.com/tier", "advanced").
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasResource("user-edit", &rbacv1.RoleBinding{}).
				HasResource("user-rbac-edit", &rbacv1.RoleBinding{})
		})

		t.Run("downgrade dev to basic tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			rb := newRoleBinding(devNS.Name, "user-edit", username)
			rbacRb := newRoleBinding(devNS.Name, "user-rbac-edit", username)
			ro := newRole(devNS.Name, "rbac-edit", username)
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, rb, rbacRb, ro)

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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11"). // downgraded
				HasLabel("toolchain.dev.openshift.com/tier", "basic").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasResource("user-edit", &rbacv1.RoleBinding{}).
				HasNoResource("rbac-edit", &rbacv1.Role{}). // role does not exist
				HasNoResource("user-rbac-edit", &rbacv1.RoleBinding{})

		})

		t.Run("delete redundant namespace while upgrading tier", func(t *testing.T) {
			// given 'advanced' NSTemplate only has a 'dev' namespace
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("basic", username, "code", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'code' NS

			// when - should delete the code namespace
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11"). // not upgraded yet
				HasLabel("toolchain.dev.openshift.com/tier", "basic")

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
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // upgraded
					HasLabel("toolchain.dev.openshift.com/tier", "advanced").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &rbacv1.RoleBinding{}).
					HasResource("rbac-edit", &rbacv1.Role{})
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "fail-dev-abcde11"). // the unknown tier that caused the error
				HasLabel("toolchain.dev.openshift.com/tier", "fail")
		})

		t.Run("fail to delete redundant namespace while upgrading tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("basic", username, "code", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'code' NS
			cl.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("mock error: '%T'", obj)
			}

			// when - should delete the code namespace
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed("mock error: '*v1.Namespace'")) // failed to delete NS
			AssertThatNamespace(t, username+"-code", cl).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "code").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-code-abcde11"). // unchanged, namespace was not deleted
				HasLabel("toolchain.dev.openshift.com/tier", "basic")
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11"). // not upgraded
				HasLabel("toolchain.dev.openshift.com/tier", "basic")
		})
	})
}

func TestUpdateNamespaces(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("success", func(t *testing.T) {

		t.Run("update from abcde11 revision to abcde12 revision as part of the advanced tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"))
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			ro := newRole(devNS.Name, "rbac-edit", username)
			rb := newRoleBinding(devNS.Name, "user-edit", username)
			rbacRb := newRoleBinding(devNS.Name, "user-rbac-edit", username)
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde12"). // upgraded
				HasLabel("toolchain.dev.openshift.com/tier", "advanced").
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasResource("user-edit", &rbacv1.RoleBinding{}).
				HasResource("rbac-edit", &rbacv1.Role{}).
				HasNoResource("user-rbac-edit", &rbacv1.RoleBinding{})
		})

		t.Run("update from abcde12 revision to abcde11 revision as part of the advanced tier", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde12"))
			rb := newRoleBinding(devNS.Name, "user-edit", username)
			ro := newRole(devNS.Name, "rbac-edit", username)
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // upgraded
				HasLabel("toolchain.dev.openshift.com/tier", "advanced").
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasResource("user-edit", &rbacv1.RoleBinding{}).
				HasResource("rbac-edit", &rbacv1.Role{}).
				HasResource("user-rbac-edit", &rbacv1.RoleBinding{})
		})

		t.Run("delete redundant namespace while updating revision", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"))
			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("advanced", username, "code", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS, codeNS) // current user has also a 'code' NS

			// when - should delete the code namespace
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
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "advanced")
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("update to abcde13 fails because it find the new template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde13", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UnableToProvisionNamespace(
					"unable to retrieve the TierTemplate 'basic-dev-abcde13' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-dev-abcde13\" not found"))
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "basic")
		})

		t.Run("update from abcde13 fails because it find current template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev"))
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde13"))
			manager, cl := prepareNamespacesManager(t, nsTmplSet, devNS)

			// when
			_, err := manager.ensure(logger, nsTmplSet)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, cl).
				HasFinalizer().
				HasConditions(UpdateFailed(
					"unable to retrieve the TierTemplate 'basic-dev-abcde13' from 'Host' cluster: tiertemplates.toolchain.dev.openshift.com \"basic-dev-abcde13\" not found"))
			AssertThatNamespace(t, username+"-dev", cl).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde13").
				HasLabel("toolchain.dev.openshift.com/tier", "basic")
		})
	})
}

func TestIsUpToDateAndProvisioned(t *testing.T) {
	// given
	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	nsTmplSet := newNSTmplSet("toolchain-member", "johnsmith", "basic", withNamespaces("abcde11", "dev", "code"))
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
		isProvisioned, err := manager.isUpToDateAndProvisioned(&devNS, tierTmpl)
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
					"toolchain.dev.openshift.com/type":        "dev",
					"toolchain.dev.openshift.com/tier":        "advanced",
					"toolchain.dev.openshift.com/templateref": "advanced-dev-abcde11",
					"toolchain.dev.openshift.com/owner":       "johnsmith",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		}
		rb := newRoleBinding(devNS.Name, "user-edit", "johnsmith")
		rb2 := newRoleBinding(devNS.Name, "user-rbac-edit", "johnsmith")
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, rb2)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(&devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("namespace doesn't have the required rolebinding", func(t *testing.T) {
		//given
		devNS := newNamespace("advanced", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "user-edit", "johnsmith")
		role := newRole(devNS.Name, "rbac-edit", "johnsmith")
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, role)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(devNS, tierTmpl)
		//then
		require.NoError(t, err)
		require.False(t, isProvisioned)
	})

	t.Run("role doesn't have the owner label", func(t *testing.T) {
		//given
		devNS := newNamespace("advanced", "johnsmith", "dev", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "user-edit", "johnsmith")
		rb2 := newRoleBinding(devNS.Name, "user-rbac-edit", "johnsmith")
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: devNS.Name,
				Name:      "rbac-edit",
				Labels: map[string]string{
					"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				},
			},
		}
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb, rb2, role)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "advanced-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(devNS, tierTmpl)
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
				Name:      "user-edit",
				Labels: map[string]string{
					"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				},
			},
		}
		manager, _ := prepareNamespacesManager(t, nsTmplSet, rb)
		tierTmpl, err := getTierTemplate(manager.GetHostCluster, "basic-dev-abcde11")
		require.NoError(t, err)
		//when
		isProvisioned, err := manager.isUpToDateAndProvisioned(devNS, tierTmpl)
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
		isProvisioned, err := manager.isUpToDateAndProvisioned(devNS, tierTmpl)
		//then
		require.Error(t, err, "namespace doesn't have owner label")
		require.False(t, isProvisioned)

	})
}
