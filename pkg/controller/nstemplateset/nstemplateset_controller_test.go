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
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	t.Run("not_found", func(t *testing.T) {
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

	t.Run("namespace_not_found", func(t *testing.T) {
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

	t.Run("should no return any namespace", func(t *testing.T) {
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

	t.Run("request_namespace", func(t *testing.T) {
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "any-name",
				Namespace: namespaceName,
			},
		}

		// test
		nsName, err := getNamespaceName(req)

		require.NoError(t, err)
		assert.Equal(t, namespaceName, nsName)
	})

	t.Run("watch_namespace", func(t *testing.T) {
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

		// test
		nsName, err := getNamespaceName(req)

		require.NoError(t, err)
		assert.Equal(t, namespaceName, nsName)
	})

	t.Run("no_namespace", func(t *testing.T) {
		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "any-name",
				Namespace: "", // blank
			},
		}

		// test
		nsName, err := getNamespaceName(req)

		require.Error(t, err)
		assert.Equal(t, "", nsName)
	})

}

func TestReconcileProvisionOK(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("without cluster resources", func(t *testing.T) {

		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))

		t.Run("new namespace created", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasNamespaces("dev", "code").
				HasConditions(Provisioning())
			AssertThatNamespace(t, username+"-dev", r.client).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "dev")
		})

		t.Run("new namespace created with existing namespace", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			// create dev
			createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasNamespaces("dev", "code").
				HasConditions(Provisioning())
			AssertThatNamespace(t, username+"-code", r.client).
				HasNoOwnerReference().
				HasLabel("toolchain.dev.openshift.com/owner", username).
				HasLabel("toolchain.dev.openshift.com/type", "code")
		})

		t.Run("set_label_for_nstemplateset", func(t *testing.T) {
			// given
			nsTmplSet.Labels = nil
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			createNamespace(t, fakeClient, "", "basic", username, "dev")

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain")
		})

		t.Run("inner resources created for existing namespace", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			// create dev
			createNamespace(t, fakeClient, "", "basic", username, "dev")

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasNamespaces("dev", "code").
				HasConditions(Provisioning())
			AssertThatNamespace(t, username+"-dev", fakeClient).
				HasResource("user-edit", &authv1.RoleBinding{})
		})

		t.Run("status provisioned", func(t *testing.T) {
			// given
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
			// create namesapces
			createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
			createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasNamespaces("dev", "code").
				HasConditions(Provisioned())
		})

		t.Run("no nstmplset available", func(t *testing.T) {
			// given
			r, req, _ := prepareReconcile(t, namespaceName, username)

			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
		})
	})

}

func TestReconcileUpdate(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("update dev to advanced tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev"))
		nsTmplSet.Spec.TierName = "advanced"
		rb := newRoleBinding(username+"-dev", "user-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb)
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")

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
			HasLabel("toolchain.dev.openshift.com/type", "dev")

		role := &v1.Role{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "toolchain-dev-edit"), role)
		require.NoError(t, err)

		binding := &v1.RoleBinding{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "user-edit"), binding)
		require.NoError(t, err)
	})

	t.Run("downgrade dev to basic tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev"))
		rb := newRoleBinding(username+"-dev", "user-edit")
		ro := newRole(username+"-dev", "toolchain-dev-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb, ro)
		createNamespace(t, fakeClient, "abcde11", "advanced", username, "dev")

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
			HasLabel("toolchain.dev.openshift.com/type", "dev")

		role := &v1.Role{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "toolchain-dev-edit"), role)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))

		binding := &v1.RoleBinding{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "user-edit"), binding)
		require.NoError(t, err)
	})

	t.Run("promotion to another tier fails because it cannot load current template", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev"))
		nsTmplSet.Spec.TierName = "advanced"
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "abcde11", "fail", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UpdateFailed("failed to to retrieve template of the current tier 'fail' for namespace 'johnsmith-dev': failed to to retrieve template for namespace"))
		AssertThatNamespace(t, username+"-dev", r.client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev")
	})

	t.Run("downgrade dev to basic tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev"))
		rb := newRoleBinding(username+"-dev", "user-edit")
		ro := newRole(username+"-dev", "toolchain-dev-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb, ro)
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("mock error")
		}
		createNamespace(t, fakeClient, "abcde11", "advanced", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UpdateFailed("failed to delete object 'toolchain-dev-edit' in namespace 'johnsmith-dev': mock error"))
		AssertThatNamespace(t, username+"-dev", r.client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev")
	})

	t.Run("delete redundant namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev"))
		nsTmplSet.Spec.TierName = "advanced"
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
		createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

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
			HasLabel("toolchain.dev.openshift.com/type", "dev")

		ns := &corev1.Namespace{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName("", username+"-code"), ns)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))
	})

	t.Run("delete redundant namespace fails", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withTierName("advanced"), withNamespaces("dev"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("mock error")
		}
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
		createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UpdateFailed("mock error"))

		AssertThatNamespace(t, username+"-dev", r.client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev")
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))

	t.Run("fail_create_namespace", func(t *testing.T) {
		// given
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
	})

	t.Run("fail_create_inner_resources", func(t *testing.T) {
		// given
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "", "basic", username, "dev")
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
	})

	t.Run("fail_update_status_for_inner_resources", func(t *testing.T) {
		// given
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "", "basic", username, "dev")
		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update NSTmlpSet")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update NSTmlpSet")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("unable to update NSTmlpSet"))
	})

	t.Run("fail_list_namespace", func(t *testing.T) {
		// given
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return errors.New("unable to list namespaces")
		}

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to list namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvision("unable to list namespaces"))
	})

	t.Run("fail_get_nstmplset", func(t *testing.T) {
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

	t.Run("fail_status_provisioning", func(t *testing.T) {
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

	t.Run("failed to get template for namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("fail"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to to retrieve template for namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("failed to to retrieve template for namespace"))
	})

	t.Run("failed to get template for inner resources", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("fail"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "", "basic", username, "fail")

		// when
		res, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to to retrieve template for namespace")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasConditions(UnableToProvisionNamespace("failed to to retrieve template for namespace"))
	})

	t.Run("no_namespace", func(t *testing.T) {
		r, _ := prepareController(t)
		req := newReconcileRequestWithNamespace(username, "")

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

	t.Run("status_updated", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))
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
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"), withConditions(conditions...))
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
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))
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
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"), withConditions(failed))
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
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))
		nsTmplSet.Status.Conditions = []toolchainv1alpha1.Condition{failed}
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, r.client, "abcde11", "basic", username, "dev")
		createNamespace(t, r.client, "abcde11", "basic", username, "code")

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
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"))
		deletionTS := metav1.NewTime(time.Now())
		nsTmplSet.SetDeletionTimestamp(&deletionTS) // mark resource as deleted
		r, req, c := prepareReconcile(t, namespaceName, username, nsTmplSet)
		for _, ns := range nsTmplSet.Spec.Namespaces {
			createNamespace(t, r.client, ns.Revision, "basic", username, ns.Type)
		}
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
			// get the first namespace and check its deletion timestamp
			firstNS := corev1.Namespace{}
			firstNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[0].Type)
			err = r.client.Get(context.TODO(), types.NamespacedName{
				Name: firstNSName,
			}, &firstNS)
			require.NoError(t, err)
			assert.NotNil(t, firstNS.GetDeletionTimestamp(), "expected a deletion timestamp on '%s' namespace", firstNSName)
			// get the NSTemplateSet resource again and check its status
			AssertThatNSTemplateSet(t, namespaceName, username, r.client).
				HasFinalizer(). // the finalizer should NOt have been removed yet
				HasConditions(Terminating())

			t.Run("reconcile after first user namespace deletion", func(t *testing.T) {
				// given a second reconcile loop was triggered (because a user namespace was deleted)
				_, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)
				// when
				_, err := r.Reconcile(req)
				// then
				require.NoError(t, err)
				// get the second namespace and check its deletion timestamp
				secondNS := corev1.Namespace{}
				secondtNSName := fmt.Sprintf("%s-%s", username, nsTmplSet.Spec.Namespaces[1].Type)
				err = r.client.Get(context.TODO(), types.NamespacedName{
					Name: secondtNSName,
				}, &secondNS)
				require.NoError(t, err)
				assert.NotNil(t, secondNS.GetDeletionTimestamp(), "expected a deletion timestamp on '%s' namespace", secondtNSName)
				// get the NSTemplateSet resource again and check its finalizers and status
				AssertThatNSTemplateSet(t, namespaceName, username, r.client).
					HasFinalizer(). // the finalizer should not have been removed either
					HasConditions(Terminating())

				t.Run("reconcile after second user namespace deletion", func(t *testing.T) {
					// given a second reconcile loop was triggered (because a user namespace was deleted)
					_, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)
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

	t.Run("without any user namespace to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, withNamespaces("dev", "code"), withDeletionTs())
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

}

func createNamespace(t *testing.T, client client.Client, revision, tier, username, typeName string) *corev1.Namespace {
	nsName := fmt.Sprintf("%s-%s", username, typeName)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/tier":     tier,
				"toolchain.dev.openshift.com/owner":    username,
				"toolchain.dev.openshift.com/revision": revision,
				"toolchain.dev.openshift.com/type":     typeName,
			},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	err := client.Create(context.TODO(), ns)
	require.NoError(t, err)
	return ns
}

func checkInnerResources(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	roleBinding := &authv1.RoleBinding{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "user-edit", Namespace: nsName}, roleBinding)
	require.NoError(t, err)
}

func newNSTmplSet(namespaceName, name string, options ...nsTmplSetOption) *toolchainv1alpha1.NSTemplateSet {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  namespaceName,
			Name:       name,
			Finalizers: []string{toolchainv1alpha1.FinalizerName},
			Labels: map[string]string{
				toolchainv1alpha1.ProviderLabelKey: toolchainv1alpha1.ProviderLabelValue,
			},
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName:   "basic",
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{},
		},
	}
	for _, set := range options {
		set(nsTmplSet)
	}
	return nsTmplSet
}

type nsTmplSetOption func(*toolchainv1alpha1.NSTemplateSet)

func withTierName(name string) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Spec.TierName = name
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

func withConditions(conditions ...toolchainv1alpha1.Condition) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Status.Conditions = conditions
	}
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
	return r, fakeClient
}

func newReconcileRequest(namespaceName, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespaceName,
			Name:      name,
		},
	}
}

func newReconcileRequestWithNamespace(name, namespace string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func newRoleBinding(namespace, name string) *v1.RoleBinding {
	return &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func newRole(namespace, name string) *v1.Role {
	return &v1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func getTemplateContent(decoder runtime.Decoder) func(tierName, typeName string) (*templatev1.Template, error) {
	return func(tierName, typeName string) (*templatev1.Template, error) {
		if typeName == "fail" || tierName == "fail" {
			return nil, fmt.Errorf("failed to to retrieve template for namespace")
		}
		var tmplContent string
		if tierName == "basic" {
			tmplContent = test.CreateTemplate(test.WithObjects(ns, rb), test.WithParams(username))
		} else {
			tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role), test.WithParams(username))
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
    labels:
      provider: codeready-toolchain
      project: codeready-toolchain
    name: ${USERNAME}-nsType
`
	rb test.TemplateObject = `
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    labels:
      provider: codeready-toolchain
      app: codeready-toolchain
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
    labels:
      provider: codeready-toolchain
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
)
