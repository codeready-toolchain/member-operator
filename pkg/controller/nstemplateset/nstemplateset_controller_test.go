package nstemplateset

import (
	"context"
	"fmt"
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

	t.Run("mistach_revision", func(t *testing.T) {
		// test
		tcNS, userNS, found := nextMissingNamespace(tcNamespaces, userNamespaces, username)

		assert.True(t, found)
		assert.Equal(t, "code", tcNS.Type)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("found_next_namespace", func(t *testing.T) {
		userNamespaces[1].Labels = map[string]string{"revision": "rev1"}

		// test
		tcNS, userNS, found := nextMissingNamespace(tcNamespaces, userNamespaces, username)

		assert.True(t, found)
		assert.Equal(t, "stage", tcNS.Type)
		assert.Nil(t, userNS)
	})

	t.Run("not_found_next_namespace", func(t *testing.T) {
		userNamespaces = append(userNamespaces, corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{"revision": "rev1"},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})

		// test
		_, _, found := nextMissingNamespace(tcNamespaces, userNamespaces, username)

		assert.False(t, found)
	})
}

func TestCreateReconcile(t *testing.T) {
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
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		nsName := fmt.Sprintf("%s-dev", username)
		activate(t, fakeClient, nsName)

		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		// for code
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		nsName = fmt.Sprintf("%s-code", username)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("with_existing_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		nsName := fmt.Sprintf("%s-dev", username)
		createNamespace(t, fakeClient, username, nsName, "rev1")

		// for code
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		nsName = fmt.Sprintf("%s-code", username)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("with_existing_old_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, username, nsTmplSet)

		nsName := fmt.Sprintf("%s-dev", username)
		createNamespace(t, fakeClient, username, nsName, "rev1")
		nsTmplSet.Spec.Namespaces[0].Revision = "rev2" // change revision to latest
		err := fakeClient.Update(context.TODO(), nsTmplSet)
		require.NoError(t, err)

		// for dev
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		// for code
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")
		nsName = fmt.Sprintf("%s-code", username)
		activate(t, fakeClient, nsName)
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionFalse, "Provisioning")

		// done
		reconcile(r, req)
		checkStatus(t, fakeClient, username, corev1.ConditionTrue, "Provisioned")
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
		client: fakeClient,
		scheme: s,
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
