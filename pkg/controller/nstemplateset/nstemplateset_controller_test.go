package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"k8s.io/api/rbac/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	authv1 "github.com/openshift/api/authorization/v1"
	templatev1 "github.com/openshift/api/template/v1"
	corev1 "k8s.io/api/core/v1"
	apierros "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")

	reconcile := func(r *NSTemplateSetReconciler, req reconcile.Request) {
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	}

	t.Run("new_namespace_created_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Provisioning")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)
	})

	t.Run("new_namespace_created_with_existing_namespace_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// create dev
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Provisioning")
		checkNamespace(t, r.client, username, "code")
	})

	t.Run("inner_resources_created_for_existing_namespace_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// create dev
		namespace := createNamespace(t, fakeClient, "", "basic", username, "dev")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())
	})

	t.Run("status_provisioned_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// create namesapces
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
		createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionTrue, namespaceName, username, "Provisioned")
	})

	t.Run("nstmplset_not_found", func(t *testing.T) {
		r, req, _ := prepareReconcile(t, namespaceName, username)

		// test
		reconcile(r, req)
	})
}

func TestReconcileUpdate(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("update dev to advanced tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		nsTmplSet.Spec.TierName = "advanced"
		rb := newRoleBinding(username+"-dev", "user-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb)
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Updating")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)

		role := &v1.Role{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "toolchain-dev-edit"), role)
		require.NoError(t, err)

		binding := &v1.RoleBinding{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName(username+"-dev", "user-edit"), binding)
		require.NoError(t, err)
	})

	t.Run("downgrade dev to basic tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		rb := newRoleBinding(username+"-dev", "user-edit")
		ro := newRole(username+"-dev", "toolchain-dev-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb, ro)
		createNamespace(t, fakeClient, "abcde11", "advanced", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Updating")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)

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
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		nsTmplSet.Spec.TierName = "advanced"
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "abcde11", "fail", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		checkStatus(t, fakeClient, namespaceName, username, "UpdateFailed")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)
	})

	t.Run("downgrade dev to basic tier", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		rb := newRoleBinding(username+"-dev", "user-edit")
		ro := newRole(username+"-dev", "toolchain-dev-edit")
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, rb, ro)
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("error")
		}
		createNamespace(t, fakeClient, "abcde11", "advanced", username, "dev")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		checkStatus(t, fakeClient, namespaceName, username, "UpdateFailed")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)
	})

	t.Run("delete redundant namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		nsTmplSet.Spec.TierName = "advanced"
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
		createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		checkReadyCond(t, fakeClient, corev1.ConditionFalse, namespaceName, username, "Updating")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)

		ns := &corev1.Namespace{}
		err = fakeClient.Get(context.TODO(), test.NamespacedName("", username+"-code"), ns)
		require.Error(t, err)
		assert.True(t, apierros.IsNotFound(err))
	})

	t.Run("delete redundant namespace fails", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev")
		nsTmplSet.Spec.TierName = "advanced"
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockDelete = func(ctx context.Context, obj runtime.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("error")
		}
		createNamespace(t, fakeClient, "abcde11", "basic", username, "dev")
		createNamespace(t, fakeClient, "abcde11", "basic", username, "code")

		// when
		_, err := r.Reconcile(req)

		// then
		require.Error(t, err)
		checkStatus(t, fakeClient, namespaceName, username, "UpdateFailed")
		checkNamespace(t, r.client, username, "dev")
		checkFinalizers(t, fakeClient, nsTmplSet.Namespace, nsTmplSet.Name)
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")

	reconcile := func(r *NSTemplateSetReconciler, req reconcile.Request, errMsg string) {
		res, err := r.Reconcile(req)
		require.Error(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		assert.Contains(t, err.Error(), errMsg)
	}

	t.Run("fail_create_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create namespace")
		}

		// test
		reconcile(r, req, "unable to create namespace")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvisionNamespace")
	})

	t.Run("fail_create_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		createNamespace(t, fakeClient, "", "basic", username, "dev")

		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
			return errors.New("unable to create some object")
		}

		// test
		reconcile(r, req, "unable to create some object")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvisionNamespace")
	})

	t.Run("fail_update_status_for_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		createNamespace(t, fakeClient, "", "basic", username, "dev")

		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update NSTmlpSet")
		}

		// test
		reconcile(r, req, "unable to update NSTmlpSet")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvisionNamespace")
	})

	t.Run("fail_list_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, list runtime.Object, opts ...client.ListOption) error {
			return errors.New("unable to list namespace")
		}

		// test
		reconcile(r, req, "unable to list namespace")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvision")
	})

	t.Run("fail_get_nstmplset", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
			return errors.New("unable to get NSTemplate")
		}

		// test
		reconcile(r, req, "unable to get NSTemplate")
	})

	t.Run("fail_status_provisioning", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update status")
		}

		// test
		reconcile(r, req, "unable to update status")
	})

	t.Run("fail_get_template_for_namespace", func(t *testing.T) {
		nsTmplSetObj := &toolchainv1alpha1.NSTemplateSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      username,
				Namespace: namespaceName,
			},
			Spec: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
					{Type: "fail", Revision: "abcde31", Template: ""},
				},
			},
		}
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSetObj)

		// test
		reconcile(r, req, "failed to to retrieve template for namespace")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvisionNamespace")
	})

	t.Run("fail_get_template_for_inner_resource", func(t *testing.T) {
		nsTmplSetObj := &toolchainv1alpha1.NSTemplateSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      username,
				Namespace: namespaceName,
			},
			Spec: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
					{Type: "fail", Revision: "abcde31", Template: ""},
				},
			},
		}
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSetObj)

		createNamespace(t, fakeClient, "", "basic", username, "fail")

		// test
		reconcile(r, req, "failed to to retrieve template for namespace")

		checkStatus(t, fakeClient, namespaceName, username, "UnableToProvisionNamespace")
	})

	t.Run("no_namespace", func(t *testing.T) {
		r, _ := prepareController(t)
		req := newReconcileRequestWithNamespace(username, "")

		// test
		reconcile(r, req, "WATCH_NAMESPACE must be set")
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
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		reconciler, _ := prepareController(t, nsTmplSet)
		condition := toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}

		// test
		err := reconciler.updateStatusConditions(nsTmplSet, condition)

		require.NoError(t, err)
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions, condition)
	})

	t.Run("status_not_updated_because_not_changed", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		reconciler, _ := prepareController(t, nsTmplSet)
		conditions := []toolchainv1alpha1.Condition{{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionFalse,
		}}
		nsTmplSet.Status.Conditions = conditions

		// test
		err := reconciler.updateStatusConditions(nsTmplSet, conditions...)

		require.NoError(t, err)
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions)
	})

	t.Run("status_error_wrapped", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		reconciler, _ := prepareController(t, nsTmplSet)
		log := logf.Log.WithName("test")

		t.Run("status_updated", func(t *testing.T) {
			statusUpdater := func(nsTmplSet *toolchainv1alpha1.NSTemplateSet, message string) error {
				assert.Equal(t, "oopsy woopsy", message)
				return nil
			}

			// test
			err := reconciler.wrapErrorWithStatusUpdate(log, nsTmplSet, statusUpdater, apierros.NewBadRequest("oopsy woopsy"), "failed to create namespace")

			require.Error(t, err)
			assert.Equal(t, "failed to create namespace: oopsy woopsy", err.Error())
		})

		t.Run("status_update_failed", func(t *testing.T) {
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
	failedCond := toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
		Message: "Operation cannot be fulfilled on namespaces bla bla bla",
	}
	provisionedCond := toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.NSTemplateSetProvisionedReason,
	}
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("when status is set to false with message, then next update to true should remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		nsTmplSet.Status.Conditions = []toolchainv1alpha1.Condition{failedCond}
		reconciler, _ := prepareController(t, nsTmplSet)

		// when
		err := reconciler.setStatusReady(nsTmplSet)

		// then
		require.NoError(t, err)
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions, provisionedCond)
	})

	t.Run("when status is set to false with message, then next successful reconcile should update it to true and remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		nsTmplSet.Status.Conditions = []toolchainv1alpha1.Condition{failedCond}
		r, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)
		createNamespace(t, r.client, "abcde11", "basic", username, "dev")
		createNamespace(t, r.client, "abcde11", "basic", username, "code")

		// when
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions, provisionedCond)
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
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
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
			updateNSTemplateSet := toolchainv1alpha1.NSTemplateSet{}
			err = r.client.Get(context.TODO(), types.NamespacedName{
				Namespace: nsTmplSet.Namespace,
				Name:      nsTmplSet.Name,
			}, &updateNSTemplateSet)
			require.NoError(t, err)
			test.AssertConditionsMatch(t, updateNSTemplateSet.Status.Conditions, toolchainv1alpha1.Condition{
				Type:   toolchainv1alpha1.ConditionReady,
				Status: corev1.ConditionFalse,
				Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
			})
			// and the finalizer should NOt have been removed yet
			assert.Equal(t, []string{toolchainv1alpha1.FinalizerName}, updateNSTemplateSet.Finalizers)

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
				updateNSTemplateSet := toolchainv1alpha1.NSTemplateSet{}
				err = r.client.Get(context.TODO(), types.NamespacedName{
					Namespace: nsTmplSet.Namespace,
					Name:      nsTmplSet.Name,
				}, &updateNSTemplateSet)
				// then
				require.NoError(t, err)
				require.Len(t, updateNSTemplateSet.Finalizers, 1)
				assert.Equal(t, toolchainv1alpha1.FinalizerName, updateNSTemplateSet.Finalizers[0])
				test.AssertConditionsMatch(t, updateNSTemplateSet.Status.Conditions, toolchainv1alpha1.Condition{
					Type:   toolchainv1alpha1.ConditionReady,
					Status: corev1.ConditionFalse,
					Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
				})

				t.Run("reconcile after second user namespace deletion", func(t *testing.T) {
					// given a second reconcile loop was triggered (because a user namespace was deleted)
					_, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)
					// when
					_, err := r.Reconcile(req)
					// then
					require.NoError(t, err)
					// get the NSTemplateSet resource again and check its finalizers and status
					updateNSTemplateSet := toolchainv1alpha1.NSTemplateSet{}
					err = r.client.Get(context.TODO(), types.NamespacedName{
						Namespace: nsTmplSet.Namespace,
						Name:      nsTmplSet.Name,
					}, &updateNSTemplateSet)
					// then
					require.NoError(t, err)
					assert.Empty(t, updateNSTemplateSet.Finalizers)
					test.AssertConditionsMatch(t, updateNSTemplateSet.Status.Conditions, toolchainv1alpha1.Condition{
						Type:   toolchainv1alpha1.ConditionReady,
						Status: corev1.ConditionFalse,
						Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
					})
				})
			})
		})
	})

	t.Run("without any user namespace to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, "dev", "code")
		deletionTS := metav1.NewTime(time.Now())
		nsTmplSet.SetDeletionTimestamp(&deletionTS) // mark resource as deleted
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

func checkReadyCond(t *testing.T, client *test.FakeClient, wantStatus corev1.ConditionStatus, namespaceName, username, wantReason string) {
	t.Helper()

	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: namespaceName}, nsTmplSet)
	require.NoError(t, err)
	wantCond := toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: wantStatus,
		Reason: wantReason,
	}
	test.AssertConditionsMatch(t, nsTmplSet.Status.Conditions, wantCond)
}

func checkNamespace(t *testing.T, cl client.Client, username, typeName string) {
	t.Helper()

	// TODO uncomment below code when issue fixed.
	// Issue: https://github.com/kubernetes-sigs/controller-runtime/issues/524

	// uncomment below code block when issue fixed
	// labels := map[string]string{"owner": username, "type": typeName}
	// opts := client.MatchingLabels(labels)
	// namespaceList := &corev1.NamespaceList{}
	// err := cl.List(context.TODO(), opts, namespaceList)
	// require.NoError(t, err)
	// require.NotNil(t, namespaceList)
	// require.Equal(t, 1, len(namespaceList.Items))
	// return &namespaceList.Items[0]

	// remove below code block when issue fixed
	namespace := &corev1.Namespace{}
	nsName := fmt.Sprintf("%s-%s", username, typeName)
	err := cl.Get(context.TODO(), types.NamespacedName{Name: nsName}, namespace)
	require.NoError(t, err)
	assert.Equal(t, username, namespace.Labels["toolchain.dev.openshift.com/owner"])
	assert.Equal(t, typeName, namespace.Labels["toolchain.dev.openshift.com/type"])
	assert.Empty(t, namespace.OwnerReferences) // namespace has not explicit owner reference.
}

func checkInnerResources(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	roleBinding := &authv1.RoleBinding{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "user-edit", Namespace: nsName}, roleBinding)
	require.NoError(t, err)
}

func checkStatus(t *testing.T, client *test.FakeClient, namespaceName, name, wantReason string) {
	t.Helper()

	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespaceName}, nsTmplSet)
	require.NoError(t, err)
	readyCond, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	assert.True(t, found)
	assert.Equal(t, corev1.ConditionFalse, readyCond.Status)
	assert.Equal(t, wantReason, readyCond.Reason)
}

func checkFinalizers(t *testing.T, client *test.FakeClient, namespaceName, name string) {
	t.Helper()
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespaceName}, nsTmplSet)
	require.NoError(t, err)
	require.Len(t, nsTmplSet.Finalizers, 1)
	assert.Equal(t, toolchainv1alpha1.FinalizerName, nsTmplSet.Finalizers[0])

}

func newNSTmplSet(namespaceName, name string, nsTypes ...string) *toolchainv1alpha1.NSTemplateSet {
	nss := make([]toolchainv1alpha1.NSTemplateSetNamespace, len(nsTypes))
	for index, nsType := range nsTypes {
		nss[index] = toolchainv1alpha1.NSTemplateSetNamespace{Type: nsType, Revision: "abcde11", Template: ""}
	}
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  namespaceName,
			Name:       name,
			Finalizers: []string{toolchainv1alpha1.FinalizerName},
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName:   "basic",
			Namespaces: nss,
		},
	}
	return nsTmplSet
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
