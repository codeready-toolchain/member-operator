package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/condition"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	authv1 "github.com/openshift/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	namespaceName = "toolchain-member"
)

func TestFindNamespace(t *testing.T) {
	namespaces := []corev1.Namespace{
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-dev"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-code"}},
	}

	t.Run("found", func(t *testing.T) {
		nsName := "johnsmith-dev"
		namespace, found := findNamespace(namespaces, nsName)
		assert.True(t, found)
		assert.Equal(t, nsName, namespace.GetName())
	})

	t.Run("not_found", func(t *testing.T) {
		nsName := "johnsmith-stage"
		_, found := findNamespace(namespaces, nsName)
		assert.False(t, found)
	})
}

func TestNextMissingNamespace(t *testing.T) {
	username := "johnsmith"
	userNamespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev", Labels: map[string]string{"revision": "rev1"},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-code",
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
	tcNamespaces := []toolchainv1alpha1.Namespace{
		{Type: "dev", Revision: "rev1"},
		{Type: "code", Revision: "rev1"},
		{Type: "stage", Revision: "rev1"},
	}

	// test mismatch_revision
	tcNS, userNS, found := nextNamespaceToProvision(tcNamespaces, userNamespaces, username)
	assert.True(t, found)
	assert.Equal(t, "code", tcNS.Type)
	assert.Equal(t, "johnsmith-code", userNS.GetName())

	// test found_next_namespace
	userNamespaces[1].Labels = map[string]string{"revision": "rev1"}
	tcNS, userNS, found = nextNamespaceToProvision(tcNamespaces, userNamespaces, username)
	assert.True(t, found)
	assert.Equal(t, "stage", tcNS.Type)
	assert.Nil(t, userNS)

	// test not_found_next_namespace
	userNamespaces = append(userNamespaces, corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith-stage", Labels: map[string]string{"revision": "rev1"},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})
	_, _, found = nextNamespaceToProvision(tcNamespaces, userNamespaces, username)
	assert.False(t, found)
}

func TestReconcileProvisionOK(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	username := "johnsmith"
	nsTmplSet := newNSTmplSet(username)

	reconcile := func(r *ReconcileNSTemplateSet, req reconcile.Request) {
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	}

	t.Run("ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		// for dev
		nsName := fmt.Sprintf("%s-dev", username)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, fakeClient, nsName)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkChildren(t, fakeClient, nsName)

		// for code
		nsName = fmt.Sprintf("%s-code", username)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, fakeClient, nsName)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkChildren(t, fakeClient, nsName)

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("ok_with_namespace_with_children", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		nsName := fmt.Sprintf("%s-dev", username)
		createNamespace(t, fakeClient, username, nsName, "rev1")

		// for code
		nsName = fmt.Sprintf("%s-code", username)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, fakeClient, nsName)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkChildren(t, fakeClient, nsName)

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("ok_with_namespace_without_children", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		nsName := fmt.Sprintf("%s-dev", username)
		createNamespace(t, fakeClient, username, nsName, "")

		// for dev
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkChildren(t, fakeClient, nsName)

		// for code
		nsName = fmt.Sprintf("%s-code", username)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, fakeClient, nsName)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkChildren(t, fakeClient, nsName)

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	username := "johnsmith"
	nsTmplSet := newNSTmplSet(username)

	reconcile := func(r *ReconcileNSTemplateSet, req reconcile.Request, errMsg string) {
		res, err := r.Reconcile(req)
		require.Error(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		assert.Contains(t, err.Error(), errMsg)
	}

	t.Run("fail_create_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to create namespace")
		}

		// test
		reconcile(r, req, "unable to create namespace")
	})

	t.Run("fail_create_children", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		nsName := fmt.Sprintf("%s-dev", username)
		createNamespace(t, fakeClient, username, nsName, "")

		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to create some object")
		}

		// test
		reconcile(r, req, "unable to create some object")
	})

}

func activate(t *testing.T, client client.Client, nsName string) {
	ns := &corev1.Namespace{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: nsName}, ns)
	require.NoError(t, err)
	ns.Status.Phase = corev1.NamespaceActive
	err = client.Update(context.TODO(), ns)
	require.NoError(t, err)
}

func createNamespace(t *testing.T, client client.Client, username, nsName, revision string) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nsName,
			Labels: map[string]string{"owner": username, "revision": revision},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	err := client.Create(context.TODO(), ns)
	require.NoError(t, err)
}

func checkStatus(t *testing.T, client *test.FakeClient, username string, wantStatus corev1.ConditionStatus, wantReason string) {
	t.Helper()

	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: namespaceName}, nsTmplSet)
	require.NoError(t, err)
	readyCond, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	assert.True(t, found)
	assert.Equal(t, wantStatus, readyCond.Status)
	assert.Equal(t, wantReason, readyCond.Reason)
}

func checkNamespace(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	namespace := &corev1.Namespace{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: nsName}, namespace)
	require.NoError(t, err)
}

func checkChildren(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	roleBinding := &authv1.RoleBinding{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "user-edit", Namespace: nsName}, roleBinding)
	require.NoError(t, err)
}

func newNSTmplSet(userName string) *toolchainv1alpha1.NSTemplateSet {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: namespaceName,
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName: "basic",
			Namespaces: []toolchainv1alpha1.Namespace{
				{Type: "dev", Revision: "rev1", Template: ""},
				{Type: "code", Revision: "rev1", Template: ""},
			},
		},
	}
	return nsTmplSet
}

func prepareReconcile(t *testing.T, username string, initObjs ...runtime.Object) (*ReconcileNSTemplateSet, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, initObjs...)

	r := &ReconcileNSTemplateSet{
		client:             fakeClient,
		scheme:             s,
		getTemplateContent: testTemplateContent,
	}
	return r, newReconcileRequest(username), fakeClient
}

func newReconcileRequest(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespaceName,
		},
	}
}

func testTemplateContent(tierName, typeName string) ([]byte, error) {
	tmplFile, err := filepath.Abs(filepath.Join("test-files", fmt.Sprintf("%s-%s.yaml", tierName, typeName)))
	if err != nil {
		return nil, err
	}
	return ioutil.ReadFile(tmplFile)
}
