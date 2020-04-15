package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"

	authv1 "github.com/openshift/api/authorization/v1"
	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
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
	// given
	userNamespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev", Labels: map[string]string{
					"toolchain.dev.openshift.com/tier":     "basic",
					"toolchain.dev.openshift.com/revision": "abcde11",
					"toolchain.dev.openshift.com/type":     "dev",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-code", Labels: map[string]string{
					"toolchain.dev.openshift.com/type": "code",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
	nsTemplateSet := &toolchainv1alpha1.NSTemplateSet{
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName: "basic",
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
				{Type: "dev", Revision: "abcde11"},
				{Type: "code", Revision: "abcde21"},
				{Type: "stage", Revision: "abcde31"},
			},
		},
	}

	t.Run("return namespace whose revision is not set", func(t *testing.T) {
		// when
		tcNS, userNS, found := nextNamespaceToProvisionOrUpdate(nsTemplateSet, userNamespaces)

		// then
		assert.True(t, found)
		assert.Equal(t, "code", tcNS.Type)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace whose revision is different than in tier", func(t *testing.T) {
		// given
		userNamespaces[1].Labels["toolchain.dev.openshift.com/revision"] = "123"
		userNamespaces[1].Labels["toolchain.dev.openshift.com/tier"] = "basic"

		// when
		tcNS, userNS, found := nextNamespaceToProvisionOrUpdate(nsTemplateSet, userNamespaces)

		// then
		assert.True(t, found)
		assert.Equal(t, "code", tcNS.Type)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace whose tier is different", func(t *testing.T) {
		// given
		userNamespaces[1].Labels["toolchain.dev.openshift.com/revision"] = "abcde21"
		userNamespaces[1].Labels["toolchain.dev.openshift.com/tier"] = "advanced"

		// when
		tcNS, userNS, found := nextNamespaceToProvisionOrUpdate(nsTemplateSet, userNamespaces)

		// then
		assert.True(t, found)
		assert.Equal(t, "code", tcNS.Type)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("return namespace that is not part of user namespaces", func(t *testing.T) {
		// given
		userNamespaces[1].Labels["toolchain.dev.openshift.com/revision"] = "abcde21"
		userNamespaces[1].Labels["toolchain.dev.openshift.com/tier"] = "basic"

		// when
		tcNS, userNS, found := nextNamespaceToProvisionOrUpdate(nsTemplateSet, userNamespaces)

		// then
		assert.True(t, found)
		assert.Equal(t, "stage", tcNS.Type)
		assert.Nil(t, userNS)
	})

	t.Run("namespace not found", func(t *testing.T) {
		// given
		userNamespaces[1].Labels["toolchain.dev.openshift.com/revision"] = "abcde21"
		userNamespaces[1].Labels["toolchain.dev.openshift.com/tier"] = "basic"
		userNamespaces = append(userNamespaces, corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{
					"toolchain.dev.openshift.com/tier":     "basic",
					"toolchain.dev.openshift.com/revision": "abcde31",
					"toolchain.dev.openshift.com/type":     "stage",
				},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})

		// when
		_, _, found := nextNamespaceToProvisionOrUpdate(nsTemplateSet, userNamespaces)

		// then
		assert.False(t, found)
	})
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
		tcNamespaces := []toolchainv1alpha1.NSTemplateSetNamespace{
			{Type: "dev", Revision: "abcde11"},
		}

		// when
		namespace, found := nextNamespaceToDeprovision(tcNamespaces, userNamespaces)

		// then
		assert.True(t, found)
		assert.Equal(t, "johnsmith-code", namespace.Name)
	})

	t.Run("should not return any namespace", func(t *testing.T) {
		// given
		tcNamespaces := []toolchainv1alpha1.NSTemplateSetNamespace{
			{Type: "dev", Revision: "abcde11"},
			{Type: "code", Revision: "abcde11"},
		}

		// when
		namespace, found := nextNamespaceToDeprovision(tcNamespaces, userNamespaces)

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
		currWatchNs := os.Getenv(k8sutil.WatchNamespaceEnvVar)
		err := os.Setenv(k8sutil.WatchNamespaceEnvVar, namespaceName)
		require.NoError(t, err)
		defer func() {
			if currWatchNs == "" {
				err := os.Unsetenv(k8sutil.WatchNamespaceEnvVar)
				require.NoError(t, err)
				return
			}
			err := os.Setenv(k8sutil.WatchNamespaceEnvVar, currWatchNs)
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

func TestReconcileAddFinalizer(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("add a finalizer when missing", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withoutFinalizer())
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer()
		})

		t.Run("failure", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withoutFinalizer())
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				fmt.Printf("updating object of type '%T'\n", obj)
				return fmt.Errorf("mock error")
			}

			// when
			res, err := r.Reconcile(req)

			// then
			require.Error(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				DoesNotHaveFinalizer()
		})
	})

}

func TestReconcileProvisionOK(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("without cluster resources", func(t *testing.T) {

		t.Run("new namespace created", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "code").
				HasConditions(Provisioning("provisioning the '-dev' namespace"))
			AssertThatNamespace(t, username+"-dev", r.client).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasNoLabel("toolchain.dev.openshift.com/revision").
				HasNoLabel("toolchain.dev.openshift.com/tier")
		})

		t.Run("new namespace created with existing namespace", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
			devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "code").
				HasConditions(Provisioning("provisioning the '-code' namespace"))
			AssertThatNamespace(t, username+"-code", r.client).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "code").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasNoLabel("toolchain.dev.openshift.com/revision").
				HasNoLabel("toolchain.dev.openshift.com/tier")

		})

		t.Run("inner resources created for existing namespace", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
			devNS := newNamespace("basic", username, "dev") // NS exist but it is not complete yet
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS)
			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "code").
				HasConditions(Provisioning("provisioning the '-dev' namespace"))
			AssertThatNamespace(t, username+"-dev", fakeClient).
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
				HasResource("user-edit", &authv1.RoleBinding{})
		})

		t.Run("status provisioned", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
			// create namespaces (and assume they are complete since they have the expected revision number)
			devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
			codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, codeNS)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "code").
				HasConditions(Provisioned())
			AssertThatNamespace(t, username+"-dev", fakeClient).
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
			AssertThatNamespace(t, username+"-code", fakeClient).
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "code").
				HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
				HasLabel("toolchain.dev.openshift.com/tier", "basic").
				HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
		})

		t.Run("no NSTemplateSet available", func(t *testing.T) {
			// given
			r, req, _ := prepareReconcile(t, namespaceName, username)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
		})
	})

	t.Run("with cluster resources", func(t *testing.T) {

		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withClusterResources())

		t.Run("status provisioning after creating cluster resources", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasConditions(Provisioning("provisioning cluster resources"))
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
		})

		t.Run("status provisioned after all resources exist", func(t *testing.T) {
			// given
			// create cluster resource quotas
			crq := newClusterResourceQuota(username, "advanced")
			// create namespaces (and assume they are complete since they have the expected revision number)
			devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
			codeNS := newNamespace("advanced", username, "code", withRevision("abcde11"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, crq, devNS, codeNS)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "code").
				HasConditions(Provisioned())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{})
		})
	})

}

func TestReconcileUpdate(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("success", func(t *testing.T) {

		t.Run("without cluster resources", func(t *testing.T) {

			t.Run("upgrade dev to advanced tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())
				// create namespace (and assume it is complete since it has the expected revision number)
				devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
				ro := newRole(devNS.Name, "toolchain-dev-edit")
				rb := newRoleBinding(devNS.Name, "user-edit")
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, ro, rb)
				err := fakeClient.Update(context.TODO(), nsTmplSet)
				require.NoError(t, err)

				// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioning("provisioning cluster resources"))
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/tier", "basic"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{}) // role has been removed

				// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // upgraded
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{})

					// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioned())
			})

			t.Run("downgrade dev to basic tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
				// create namespace (and assume it is complete since it has the expected revision number)
				devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
				rb := newRoleBinding(devNS.Name, "user-edit")
				ro := newRole(devNS.Name, "toolchain-dev-edit")
				crq := newClusterResourceQuota(username, "advanced")
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, rb, ro, crq)

				// when
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/tier", "basic"). // "downgraded"
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{}).
					HasNoResource("toolchain-dev-edit", &rbacv1.Role{}) // role does not exist

				// when reconciling again
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioned())
				AssertThatCluster(t, fakeClient).
					HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // no cluster resource quota in 'basic` tier
			})
		})

		t.Run("with cluster resources", func(t *testing.T) {

			t.Run("upgrade dev to advanced tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())
				// create namespace (and assume it is complete since it has the expected revision number)
				devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
				ro := newRole(devNS.Name, "toolchain-dev-edit")
				rb := newRoleBinding(devNS.Name, "user-edit")
				crq := newClusterResourceQuota(username, "basic")
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, ro, rb, crq)
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{}, WithLabel("toolchain.dev.openshift.com/tier", "basic"))

				err := fakeClient.Update(context.TODO(), nsTmplSet)
				require.NoError(t, err)

				// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // upgraded
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // upgraded
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("toolchain-dev-edit", &rbacv1.Role{}).
					HasResource("user-edit", &authv1.RoleBinding{})

				// when reconciling again
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				// NSTemplateSet provisioning is complete
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioned())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/tier", "advanced").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{}) // role has been removed
			})

			t.Run("downgrade dev to basic tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
				// create namespace (and assume it is complete since it has the expected revision number)
				devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
				rb := newRoleBinding(devNS.Name, "user-edit")
				ro := newRole(devNS.Name, "toolchain-dev-edit")
				crq := newClusterResourceQuota(username, "advanced")
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, rb, ro, crq)

				// when
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // removed
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/tier", "basic"). // "downgraded"
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{}).
					HasNoResource("toolchain-dev-edit", &rbacv1.Role{}) // role does not exist

				// when reconciling again
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				// NSTemplateSet provisioning is complete
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Provisioned())
				// resource below have not changed
				AssertThatCluster(t, fakeClient).
					HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // removed
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/tier", "basic"). // "downgraded"
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &authv1.RoleBinding{}).
					HasNoResource("toolchain-dev-edit", &rbacv1.Role{}) // role does not exist

			})
		})
	})

	t.Run("failure", func(t *testing.T) {

		t.Run("promotion to another tier fails because it cannot load current template", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
			// create namespace but with an unknown tier
			devNS := newNamespace("fail", username, "dev", withRevision("abcde11"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS)

			// when
			_, err := r.Reconcile(req)

			// then
			require.Error(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasConditions(UpdateFailed("failed to retrieve template for tier/type 'fail/dev': failed to retrieve template for namespace"))
			AssertThatNamespace(t, username+"-dev", r.client).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
				HasLabel("toolchain.dev.openshift.com/type", "dev").
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
				HasLabel("toolchain.dev.openshift.com/tier", "fail") // the unknown tier that caused the error
		})

	})

	t.Run("delete redundant objects", func(t *testing.T) {

		t.Run("success", func(t *testing.T) {

			t.Run("with cluster resources", func(t *testing.T) {

				t.Run("delete redundant namespace while upgrading tier", func(t *testing.T) {
					// given 'advanced' NSTemplate only has a 'dev' namespace
					nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())
					devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
					devRole := newRole(devNS.Name, "basic-role-dev")
					codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
					codeRole := newRole(devNS.Name, "basic-role-code")
					r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, devRole, codeNS, codeRole) // current user has also a 'code' NS

					// when
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Provisioning("provisioning cluster resources")) // still in progress
					AssertThatCluster(t, r.client).
						HasResource("for-"+username, &quotav1.ClusterResourceQuota{}, WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // "upgraded"

					// when reconciling again
					_, err = r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Updating()) // still in progress
					AssertThatNamespace(t, codeNS.Name, r.client).
						DoesNotExist() // namespace was deleted
					AssertThatNamespace(t, devNS.Name, r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasLabel("toolchain.dev.openshift.com/tier", "basic") // not upgraded yet

					// when reconciling again
					_, err = r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Updating()) // still in progress, dealing with NS inner resources
					AssertThatNamespace(t, devNS.Name, r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // upgraded
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasResource("user-edit", &authv1.RoleBinding{}).
						HasResource("toolchain-dev-edit", &rbacv1.Role{})

					// when reconciling again
					_, err = r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Provisioned()) // done
				})

				t.Run("delete redundant objects in namespace while updating tmpl", func(t *testing.T) {
					// we need to compare the new template vs previous one, which we can't do for now.
					// See https://issues.redhat.com/browse/CRT-498
					t.Skip("can't do it now")
				})

			})

			t.Run("with cluster resources", func(t *testing.T) {

				t.Run("no redundant cluster resource to delete while upgrading tier", func(t *testing.T) {
					// given same as above, but not upgrading tier and no cluster resource quota to delete
					nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withClusterResources())
					basicCRQ := newClusterResourceQuota(username, "basic")                                  // resource has same name in both tiers
					r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, basicCRQ) // current bnasic NSTemplateSet also has a cluster resource quota
					fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
						// because the fake client does not support such a type of list :(
						if list, ok := list.(*unstructured.UnstructuredList); ok {
							basicCRQObj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(basicCRQ)
							list.Items = []unstructured.Unstructured{
								{
									Object: basicCRQObj,
								},
							}
							return nil
						}
						return fakeClient.Client.List(ctx, list, opts...)
					}
					// when
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Provisioned()) // done in 1 loop
					AssertThatCluster(t, r.client).
						HasResource("for-"+username, &quotav1.ClusterResourceQuota{}, WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // upgraded
				})

				t.Run("delete redundant cluster resource quota while downgrading tier", func(t *testing.T) {
					// given 'advanced' NSTemplate only has a cluster resource
					nsTmplSet := newNSTmplSet(namespaceName, username, "basic") // no cluster resources, so the "advancedCRQ" should be deleted
					advancedCRQ := newClusterResourceQuota(username, "advanced")
					r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, advancedCRQ)

					// when
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Provisioned()) // done in a single reconcile loop
					AssertThatCluster(t, r.client).
						HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})
				})

				t.Run("delete redundant cluster resource quota while updating tmpl", func(t *testing.T) {
					// we need to compare the new template vs previous one, which we can't do for now.
					// See https://issues.redhat.com/browse/CRT-498
					t.Skip("can't do it now")
				})
			})

		})

		t.Run("failure", func(t *testing.T) {

			t.Run("fail to delete redundant namespace while upgrading tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())
				devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
				codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, codeNS)
				fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
					return fmt.Errorf("mock error: '%T'", obj)
				}

				// when reconciling for the cluster resources
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err) // runs fine as there's nothing to delete

				// when reconciling for the namespaces
				_, err = r.Reconcile(req)

				// then
				require.Error(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(UpdateFailed("mock error: '*v1.Namespace'")) // failed to delete NS
				AssertThatNamespace(t, username+"-code", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "code").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasLabel("toolchain.dev.openshift.com/tier", "basic") // unchanged, namespace was not deleted
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasLabel("toolchain.dev.openshift.com/tier", "basic") // not upgraded
			})

			t.Run("fail to delete redundant objects in namespace while updating tmpl", func(t *testing.T) {
				// we need to compare the new template vs previous one, which we can't do for now.
				// See https://issues.redhat.com/browse/CRT-498
				t.Skip("can't do it now")
			})

			t.Run("fail to delete redundant cluster resource quota while downgrading tier", func(t *testing.T) {
				// given
				nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev"))
				// create namespace (and assume it is complete since it has the expected revision number)
				devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
				crq := newClusterResourceQuota(username, "advanced")
				rb := newRoleBinding(devNS.Name, "user-edit")
				ro := newRole(devNS.Name, "toolchain-dev-edit")
				r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, crq, rb, ro)
				fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
					return fmt.Errorf("mock error: '%T'", obj)
				}

				// when
				_, err := r.Reconcile(req)

				// then
				require.Error(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(UpdateFailed("failed to delete object 'for-johnsmith' of kind 'ClusterResourceQuota' in namespace '': mock error: '*unstructured.Unstructured'")) // the template objects are of type `*unstructured.Unstructured`
				AssertThatNamespace(t, username+"-dev", r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasLabel("toolchain.dev.openshift.com/tier", "advanced") // unchanged
			})

			t.Run("fail to delete redundant cluster resource quota while updating tmpl", func(t *testing.T) {
				// we need to compare the new template vs previous one, which we can't do for now.
				// See https://issues.redhat.com/browse/CRT-498
				t.Skip("can't do it now")
			})

		})
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("fail to create namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create namespace")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to create namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("unable to create resource of kind: Namespace, version: v1: unable to create resource of kind: Namespace, version: v1: unable to create namespace"))
		AssertThatNamespace(t, username+"-dev", r.client).DoesNotExist()
		AssertThatNamespace(t, username+"-code", r.client).DoesNotExist()
	})

	t.Run("fail to create inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		devNS := newNamespace("basic", username, "dev") // NS exists but is missing its inner resources (since its revision is not set yet)
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create some object")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to create some object")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("unable to create resource of kind: RoleBinding, version: v1: unable to create resource of kind: RoleBinding, version: v1: unable to create some object"))
		AssertThatNamespace(t, username+"-dev", r.client).
			HasNoResource("user-edit", &authv1.RoleBinding{})
	})

	t.Run("fail to update status for inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		devNS := newNamespace("basic", username, "dev") // NS exists but is missing its inner resources (since its revision is not set yet)
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS)
		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update NSTmplSet")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update NSTmplSet")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("unable to update NSTmplSet"))
	})

	t.Run("fail to cluster resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return errors.New("unable to list cluster resources")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to list cluster resources")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UpdateFailed("unable to list cluster resources"))
	})

	t.Run("fail to get nstmplset", func(t *testing.T) {
		// given
		r, req, fakeClient := prepareReconcile(t, namespaceName, username)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
			return errors.New("unable to get NSTemplate")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to get NSTemplate")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("fail to update status", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update status")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update status")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasNoConditions() // since we're unable to update the status
	})

	t.Run("fail to get template for namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("fail"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve template for namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("failed to retrieve template for namespace"))
	})

	t.Run("fail to get template for inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("fail"))
		failNS := newNamespace("basic", username, "fail") // NS exists but with an unknown type
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, failNS)

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve template for namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("failed to retrieve template for namespace"))
	})

	t.Run("no namespace", func(t *testing.T) {
		// given
		r, _ := prepareController(t)
		req := newReconcileRequest("", username)

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WATCH_NAMESPACE must be set")
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestUpdateStatus(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("status updated", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		reconciler, fakeClient := prepareController(t, nsTmplSet)
		condition := toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}

		// when
		err := reconciler.updateStatusConditions(nsTmplSet, condition)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(condition)
	})

	t.Run("status not updated because not changed", func(t *testing.T) {
		// given
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
		}}
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"), withConditions(conditions...))
		reconciler, fakeClient := prepareController(t, nsTmplSet)

		// when
		err := reconciler.updateStatusConditions(nsTmplSet, conditions...)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(conditions...)
	})

	t.Run("status error wrapped", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"))
		reconciler, _ := prepareController(t, nsTmplSet)
		log := logf.Log.WithName("test")

		t.Run("status_updated", func(t *testing.T) {
			// given
			statusUpdater := func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
				assert.Equal(t, "oopsy woopsy", message)
				return nil
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, nsTmplSet, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create namespace")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create namespace: oopsy woopsy", err.Error())
		})

		t.Run("status update failed", func(t *testing.T) {
			// given
			statusUpdater := func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
				return errors.New("unable to update status")
			}

			// when
			err := reconciler.wrapErrorWithStatusUpdate(log, nsTmplSet, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create namespace")

			// then
			require.Error(t, err)
			assert.Equal(t, "failed to create namespace: oopsy woopsy", err.Error())
		})
	})

	t.Run("status update failures", func(t *testing.T) {

		t.Run("failed to update status during deletion", func(t *testing.T) {
			// given an NSTemplateSet resource which is being deleted and whose finalizer was already removed
			nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withoutFinalizer(), withDeletionTs(), withClusterResources(), withNamespaces("dev", "code"))
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
				return fmt.Errorf("status update error")
			}
			// when a reconcile loop is triggered
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, r.client).
				DoesNotHaveFinalizer(). // finalizer was not added and nothing else was done
				HasConditions(Provisioned())
		})
	})
}
func TestUpdateStatusToProvisionedWhenPreviouslyWasSetToFailed(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	failed := toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
		Message: "Operation cannot be fulfilled on namespaces bla bla bla",
	}
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("when status is set to false with message, then next update to true should remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"), withConditions(failed))
		reconciler, fakeClient := prepareController(t, nsTmplSet)

		// when
		err := reconciler.setStatusReady(nsTmplSet)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
	})

	t.Run("when status is set to false with message, then next successful reconcile should update it to true and remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"), withConditions(failed))
		devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
		codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, codeNS)

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(Provisioned())
	})
}

func TestDeleteNSTemplateSet(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("with 2 user namespaces to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"), withDeletionTs())
		devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
		codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
		r, c := prepareController(t, nsTmplSet, devNS, codeNS)
		c.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			if obj, ok := obj.(*corev1.Namespace); ok {
				// mark namespaces as deleted...
				deletionTS := metav1.NewTime(time.Now())
				obj.SetDeletionTimestamp(&deletionTS)
				// ... but replace them in the fake client cache yet instead of deleting them
				return c.Client.Update(ctx, obj)
			}
			return c.Client.Delete(ctx, obj, opts...)
		}

		t.Run("reconcile after nstemplateset deletion", func(t *testing.T) {
			// given
			req := newReconcileRequest(namespaceName, username)

			// when a first reconcile loop is triggered (when the NSTemplateSet resource is marked for deletion and there's a finalizer)
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			// get the first namespace and check its deletion timestamp
			firstNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[0].Type)
			AssertThatNamespace(t, firstNSName, r.client).HasDeletionTimestamp()
			// get the NSTemplateSet resource again and check its status
			AssertThatNSTemplateSet(t, namespaceName, username, r.client).
				HasFinalizer(). // the finalizer should NOT have been removed yet
				HasConditions(Terminating())

			t.Run("reconcile after first user namespace deletion", func(t *testing.T) {
				// given
				req := newReconcileRequest(namespaceName, username)

				// when a second reconcile loop was triggered (because a user namespace was deleted)
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				// get the second namespace and check its deletion timestamp
				secondtNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[1].Type)
				AssertThatNamespace(t, secondtNSName, r.client).HasDeletionTimestamp()
				// get the NSTemplateSet resource again and check its finalizers and status
				AssertThatNSTemplateSet(t, namespaceName, username, r.client).
					HasFinalizer(). // the finalizer should not have been removed either
					HasConditions(Terminating())

				t.Run("reconcile after second user namespace deletion", func(t *testing.T) {
					// given
					req := newReconcileRequest(namespaceName, username)

					// when
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					// get the NSTemplateSet resource again and check its finalizers and status
					AssertThatNSTemplateSet(t, namespaceName, username, r.client).
						DoesNotHaveFinalizer(). // the finalizer should have been removed now
						HasConditions(Terminating())
				})
			})
		})
	})

	t.Run("with cluster resources and 2 user namespaces to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withDeletionTs(), withClusterResources())
		crq := newClusterResourceQuota(username, "advanced")
		devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
		codeNS := newNamespace("advanced", username, "code", withRevision("abcde11"))
		r, _ := prepareController(t, nsTmplSet, crq, devNS, codeNS)

		t.Run("reconcile after nstemplateset deletion", func(t *testing.T) {
			// given
			req := newReconcileRequest(namespaceName, username)

			// when a first reconcile loop was triggered (because a cluster resource quota was deleted)
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			// get the first namespace and check its deletion timestamp
			firstNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[0].Type)
			AssertThatNamespace(t, firstNSName, r.client).DoesNotExist()
			// get the NSTemplateSet resource again and check its status
			AssertThatNSTemplateSet(t, namespaceName, username, r.client).
				HasFinalizer(). // the finalizer should NOT have been removed yet
				HasConditions(Terminating())

			t.Run("reconcile after first user namespace deletion", func(t *testing.T) {
				// given
				req := newReconcileRequest(namespaceName, username)

				// when a second reconcile loop was triggered (because a user namespace was deleted)
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				// get the second namespace and check its deletion timestamp
				secondtNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[1].Type)
				AssertThatNamespace(t, secondtNSName, r.client).DoesNotExist()
				// get the NSTemplateSet resource again and check its finalizers and status
				AssertThatNSTemplateSet(t, namespaceName, username, r.client).
					HasFinalizer(). // the finalizer should not have been removed either
					HasConditions(Terminating())

				t.Run("reconcile after second user namespace deletion", func(t *testing.T) {
					// given a third reconcile loop was triggered (because a user namespace was deleted)
					req := newReconcileRequest(namespaceName, username)

					// when
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, r.client).
						HasFinalizer(). // the finalizer should NOT have been removed yet
						HasConditions(Terminating())
					AssertThatCluster(t, r.client).
						HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{}) // resource was deleted

					t.Run("reconcile after cluster resource quota deletion", func(t *testing.T) {
						// given
						req := newReconcileRequest(namespaceName, username)

						// when a last reconcile loop is triggered (when the NSTemplateSet resource is marked for deletion and there's a finalizer)
						_, err := r.Reconcile(req)

						// then
						require.NoError(t, err)
						// get the NSTemplateSet resource again and check its finalizers and status
						AssertThatNSTemplateSet(t, namespaceName, username, r.client).
							DoesNotHaveFinalizer(). // the finalizer should have been removed now
							HasConditions(Terminating())
						AssertThatCluster(t, r.client).HasNoResource(username, &quotav1.ClusterResourceQuota{})
					})
				})
			})
		})
	})

	t.Run("without any user namespace to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("dev", "code"), withDeletionTs())
		r, req, c := prepareReconcile(t, namespaceName, username, nsTmplSet)
		c.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			if obj, ok := obj.(*corev1.Namespace); ok {
				// mark namespaces as deleted...
				deletionTS := metav1.NewTime(time.Now())
				obj.SetDeletionTimestamp(&deletionTS)
				// ... but replace them in the fake client cache yet instead of deleting them
				return c.Client.Update(ctx, obj)
			}
			return c.Client.Delete(ctx, obj, opts...)
		}
		t.Run("reconcile after nstemplateset deletion", func(t *testing.T) {
			// when a first reconcile loop is triggered (when the NSTemplateSet resource is marked for deletion and there's a finalizer)
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)

			// get the NSTemplateSet resource again and check its finalizers
			updateNSTemplateSet := toolchainv1alpha1.NSTemplateSet{}
			err = r.client.Get(context.TODO(), types.NamespacedName{
				Namespace: nsTmplSet.Namespace,
				Name:      nsTmplSet.Name,
			}, &updateNSTemplateSet)
			// then
			require.NoError(t, err)
			assert.Empty(t, updateNSTemplateSet.Finalizers)
		})
	})

	t.Run("delete when there is no finalizer", func(t *testing.T) {
		// given an NSTemplateSet resource which is being deleted and whose finalizer was already removed
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withoutFinalizer(), withDeletionTs(), withClusterResources(), withNamespaces("dev", "code"))
		r, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when a reconcile loop is triggered
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, r.client).
			DoesNotHaveFinalizer() // finalizer was not added and nothing else was done
	})
}

func prepareReconcile(t *testing.T, namespaceName, name string, initObjs ...runtime.Object) (*NSTemplateSetReconciler, reconcile.Request, *test.FakeClient) {
	r, fakeClient := prepareController(t, initObjs...)
	return r, newReconcileRequest(namespaceName, name), fakeClient
}

func prepareController(t *testing.T, initObjs ...runtime.Object) (*NSTemplateSetReconciler, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()
	fakeClient := test.NewFakeClient(t, initObjs...)
	r := &NSTemplateSetReconciler{
		client:             fakeClient,
		scheme:             s,
		getTemplateContent: getTemplateContent(decoder),
	}

	// objects created from OpenShift templates are `*unstructured.Unstructured`,
	// which causes troubles when calling the `List` method on the fake client,
	// so we're explicitly converting the objects during their creation and update
	fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		return fakeClient.Client.Create(ctx, o, opts...)
	}
	fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		return fakeClient.Client.Update(ctx, o, opts...)
	}

	return r, fakeClient
}

func toStructured(obj runtime.Object, decoder runtime.Decoder) (runtime.Object, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		data, err := u.MarshalJSON()
		if err != nil {
			return nil, err
		}
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "ClusterResourceQuota":
			crq := &quotav1.ClusterResourceQuota{}
			_, _, err = decoder.Decode(data, nil, crq)
			return crq, err
		}
	}
	return obj, nil
}

func newReconcileRequest(namespaceName, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespaceName,
			Name:      name,
		},
	}
}

func newNSTmplSet(namespaceName, name, tier string, options ...nsTmplSetOption) *toolchainv1alpha1.NSTemplateSet { // nolint: unparam
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  namespaceName,
			Name:       name,
			Finalizers: []string{toolchainv1alpha1.FinalizerName},
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName:   tier,
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{},
		},
	}
	for _, set := range options {
		set(nsTmplSet)
	}
	return nsTmplSet
}

type nsTmplSetOption func(*toolchainv1alpha1.NSTemplateSet)

func withoutFinalizer() nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Finalizers = []string{}
	}
}

func withDeletionTs() nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		deletionTS := metav1.NewTime(time.Now())
		nsTmplSet.SetDeletionTimestamp(&deletionTS)
	}
}

func withNamespaces(types ...string) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nss := make([]toolchainv1alpha1.NSTemplateSetNamespace, len(types))
		for index, nsType := range types {
			nss[index] = toolchainv1alpha1.NSTemplateSetNamespace{Type: nsType, Revision: "abcde11", Template: ""}
		}
		nsTmplSet.Spec.Namespaces = nss
	}
}

func withClusterResources() nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Spec.ClusterResources = &toolchainv1alpha1.NSTemplateSetClusterResources{
			Revision: "12345bb",
			Template: "",
		}
	}
}

func withConditions(conditions ...toolchainv1alpha1.Condition) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Status.Conditions = conditions
	}
}

func newNamespace(tier, username, typeName string, options ...namespaceOption) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", username, typeName),
			Labels: map[string]string{
				"toolchain.dev.openshift.com/tier":     tier,
				"toolchain.dev.openshift.com/owner":    username,
				"toolchain.dev.openshift.com/type":     typeName,
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
			},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	for _, set := range options {
		set(ns)
	}
	return ns
}

type namespaceOption func(*corev1.Namespace)

func withRevision(revision string) namespaceOption { // nolint: unparam
	return func(ns *corev1.Namespace) {
		ns.ObjectMeta.Labels["toolchain.dev.openshift.com/revision"] = revision
	}
}

func newRoleBinding(namespace, name string) *authv1.RoleBinding { //nolint: unparam
	return &authv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
			},
		},
	}
}

func newRole(namespace, name string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
			},
		},
	}
}

func newClusterResourceQuota(username, tier string) *quotav1.ClusterResourceQuota {
	return &quotav1.ClusterResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				"toolchain.dev.openshift.com/tier":     tier,
				"toolchain.dev.openshift.com/owner":    username,
			},
			Annotations: map[string]string{},
			Name:        "for-" + username,
		},
		Spec: quotav1.ClusterResourceQuotaSpec{
			Quota: corev1.ResourceQuotaSpec{
				Hard: corev1.ResourceList{
					"limits.cpu":    resource.MustParse("1750m"),
					"limits.memory": resource.MustParse("7Gi"),
				},
			},
			Selector: quotav1.ClusterResourceQuotaSelector{
				AnnotationSelector: map[string]string{
					"openshift.io/requester": username,
				},
			},
		},
	}
}

func getTemplateContent(decoder runtime.Decoder) func(tierName, typeName string) (*templatev1.Template, error) {
	return func(tierName, typeName string) (*templatev1.Template, error) {
		if typeName == "fail" || tierName == "fail" {
			return nil, fmt.Errorf("failed to retrieve template for namespace")
		}
		var tmplContent string
		switch tierName {
		case "advanced": // assume that this tier has a "cluster resources" template
			switch typeName {
			case ClusterResources:
				tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq), test.WithParams(username))
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role), test.WithParams(username))
			}
		case "basic":
			switch typeName {
			case ClusterResources: // assume that this tier has no "cluster resources" template
				return nil, nil
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb), test.WithParams(username))
			}
		default:
			return nil, fmt.Errorf("no template for tier '%s'", tierName)
		}
		tmplContent = strings.ReplaceAll(tmplContent, "nsType", typeName)
		tmpl := &templatev1.Template{}
		_, _, err := decoder.Decode([]byte(tmplContent), nil, tmpl)
		if err != nil {
			return nil, err
		}
		return tmpl, err
	}
}

var (
	ns test.TemplateObject = `
- apiVersion: v1
  kind: Namespace
  metadata:
    name: ${USERNAME}-nsType
`
	rb test.TemplateObject = `
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    name: user-edit
    namespace: ${USERNAME}-nsType
  roleRef:
    name: edit
  subjects:
    - kind: User
      name: ${USERNAME}
  userNames:
    - ${USERNAME}`

	role test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: toolchain-dev-edit
    namespace: ${USERNAME}-nsType
  rules:
  - apiGroups:
    - authorization.openshift.io
    - rbac.authorization.k8s.io
    resources:
    - roles
    - rolebindings
    verbs:
    - '*'`

	username test.TemplateParam = `
- name: USERNAME
  value: johnsmith`

	advancedCrq test.TemplateObject = `
- apiVersion: quota.openshift.io/v1
  kind: ClusterResourceQuota
  metadata:
    name: for-${USERNAME}
  spec:
    quota:
    hard:
      limits.cpu: 1750m
      limits.memory: 7Gi
      limits.ephemeral-storage: 5Gi
      requests.cpu: 1750m
      requests.memory: 7Gi
      requests.storage: 5Gi
      requests.ephemeral-storage: 5Gi
      persistentvolumeclaims: "2"
      pods: "100"
      replicationcontrollers: "100"
      services: "100"
      secrets: "100"
      configmaps: "100"
    selector:
    annotations:
      openshift.io/requester: ${USERNAME}
    labels: null
  `
)
