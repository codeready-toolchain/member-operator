package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
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
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-dev", Labels: map[string]string{"type": "dev"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "johnsmith-code", Labels: map[string]string{"type": "code"}}},
	}

	t.Run("found", func(t *testing.T) {
		typeName := "dev"
		namespace, found := findNamespace(namespaces, typeName)
		assert.True(t, found)
		assert.NotNil(t, namespace)
		assert.Equal(t, typeName, namespace.GetLabels()["type"])
	})

	t.Run("not_found", func(t *testing.T) {
		typeName := "stage"
		_, found := findNamespace(namespaces, typeName)
		assert.False(t, found)
	})
}

func TestNextMissingNamespace(t *testing.T) {
	userNamespaces := []corev1.Namespace{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-dev", Labels: map[string]string{"owner": "johnsmith", "revision": "rev1", "type": "dev"},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-code", Labels: map[string]string{"owner": "johnsmith", "type": "code"},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	}
	tcNamespaces := []toolchainv1alpha1.Namespace{
		{Type: "dev", Revision: "rev1"},
		{Type: "code", Revision: "rev1"},
		{Type: "stage", Revision: "rev1"},
	}

	// revision not set
	tcNS, userNS, found := nextNamespaceToProvision(tcNamespaces, userNamespaces)
	assert.True(t, found)
	assert.Equal(t, "code", tcNS.Type)
	assert.Equal(t, "johnsmith-code", userNS.GetName())

	// missing namespace
	userNamespaces[1].Labels["revision"] = "rev1"
	tcNS, userNS, found = nextNamespaceToProvision(tcNamespaces, userNamespaces)
	assert.True(t, found)
	assert.Equal(t, "stage", tcNS.Type)
	assert.Nil(t, userNS)

	// namespace not found
	userNamespaces = append(userNamespaces, corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "johnsmith-stage", Labels: map[string]string{"revision": "rev1", "type": "stage"},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})
	_, _, found = nextNamespaceToProvision(tcNamespaces, userNamespaces)
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
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		namespace := checkNamespace(t, r.client, username, "dev")
		activate(t, fakeClient, namespace.GetName())
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())

		// for code
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		namespace = checkNamespace(t, fakeClient, username, "code")
		activate(t, fakeClient, namespace.GetName())
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())

		// done
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("ok_with_namespace_with_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		// create dev
		createNamespace(t, fakeClient, username, "rev1", "dev")

		// for code
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		namespace := checkNamespace(t, r.client, username, "code")
		activate(t, fakeClient, namespace.GetName())
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())

		// done
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("ok_with_namespace_without_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		// create dev
		namespace := createNamespace(t, fakeClient, username, "", "dev")

		// for dev
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())

		// for code
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		namespace = checkNamespace(t, r.client, username, "code")
		activate(t, fakeClient, namespace.GetName())
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())

		// done
		reconcile(r, req)
		checkReadyCond(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
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
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "UnableToProvisionNamespace")
	})

	t.Run("fail_create_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		createNamespace(t, fakeClient, username, "", "dev")

		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to create some object")
		}

		// test
		reconcile(r, req, "unable to create some object")
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "UnableToProvisionNamespace")
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

func createNamespace(t *testing.T, client client.Client, username, revision, typeName string) *corev1.Namespace {
	nsName := fmt.Sprintf("%s-%s", username, typeName)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nsName,
			Labels: map[string]string{"owner": username, "revision": revision, "type": typeName},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	err := client.Create(context.TODO(), ns)
	require.NoError(t, err)
	return ns
}

func checkReadyCond(t *testing.T, client *test.FakeClient, username string, wantStatus corev1.ConditionStatus, wantReason string) {
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

func checkNamespace(t *testing.T, cl client.Client, username, typeName string) *corev1.Namespace {
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
	require.Equal(t, namespace.Labels["owner"], username)
	require.Equal(t, namespace.Labels["type"], typeName)
	return namespace
}

func checkInnerResources(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	roleBinding := &authv1.RoleBinding{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "user-edit", Namespace: nsName}, roleBinding)
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

func newNSTmplSet(userName string) *toolchainv1alpha1.NSTemplateSet {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userName,
			Namespace: namespaceName,
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName: "basic",
			Namespaces: []toolchainv1alpha1.Namespace{
				{Type: "dev", Revision: "abcde11", Template: ""},
				{Type: "code", Revision: "abcde21", Template: ""},
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
