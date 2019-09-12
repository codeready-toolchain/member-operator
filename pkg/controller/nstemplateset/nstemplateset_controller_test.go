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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	projectv1 "github.com/openshift/api/project/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const (
	namespaceName = "toolchain-member"
)

func TestNSTmplSetCreateReconcile(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"

	// given
	nsTmplSet := newNSTmplSet(username)

	r, req, _ := prepareReconcile(t, username, nsTmplSet)

	res, err := r.Reconcile(req)
	require.NoError(t, err)
	fmt.Println(res)

	nsName := fmt.Sprintf("%s-dev", username)
	ns := &corev1.Namespace{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: nsName}, ns)
	require.NoError(t, err)
	ns.Status.Phase = corev1.NamespaceActive
	err = r.client.Update(context.TODO(), ns)
	require.NoError(t, err)

	res, err = r.Reconcile(req)
	require.NoError(t, err)
	fmt.Println(res)

	res, err = r.Reconcile(req)
	require.NoError(t, err)
	fmt.Println(res)

	nsName = fmt.Sprintf("%s-code", username)
	ns = &corev1.Namespace{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: nsName}, ns)
	require.NoError(t, err)
	ns.Status.Phase = corev1.NamespaceActive
	err = r.client.Update(context.TODO(), ns)
	require.NoError(t, err)

	res, err = r.Reconcile(req)
	require.NoError(t, err)
	fmt.Println(res)

	res, err = r.Reconcile(req)
	require.NoError(t, err)
	fmt.Println(res)
}

func testReconcile(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"

	// given
	nsTmplSet := newNSTmplSet(username)

	reconcile := func(r *ReconcileNSTemplateSet, req reconcile.Request) {
		_, err := r.Reconcile(req)
		require.NoError(t, err)
	}

	t.Run("create_nstmplset", func(t *testing.T) {
		r, req, client := prepareReconcile(t, username, nsTmplSet)

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, client, username, nsTmplSet.Spec.Namespaces[0])

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, client, username, nsTmplSet.Spec.Namespaces[1])

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("create_nstmplset_with_existing_namespace", func(t *testing.T) {
		r, req, client := prepareReconcile(t, username, nsTmplSet)

		// create namespace
		createNamespaceWithLabels(t, client, nsTmplSet.Spec.Namespaces[0], username)

		reconcile(r, req) // reconcile for "code" namespace
		checkStatus(t, client, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, client, username, nsTmplSet.Spec.Namespaces[1])

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("edit_nstmplset_with_existing_old_namespace", func(t *testing.T) {
		r, req, client := prepareReconcile(t, username, nsTmplSet)

		// create namespace with old revision
		createNamespaceWithLabels(t, client, nsTmplSet.Spec.Namespaces[0], username)

		// update template version
		nsTmplSet.Spec.Namespaces[0].Revision = "rev2" // change revision to latest
		err := client.Update(context.TODO(), nsTmplSet)
		require.NoError(t, err)

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, client, username, nsTmplSet.Spec.Namespaces[0])

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, client, username, nsTmplSet.Spec.Namespaces[1])

		reconcile(r, req)
		checkStatus(t, client, username, corev1.ConditionTrue, "Provisioned")
	})
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

func checkNamespace(t *testing.T, client *test.FakeClient, username string, tcNamespace toolchainv1alpha1.Namespace) {
	t.Helper()

	name := toNamespaceName(username, tcNamespace.Type)
	namespace := &corev1.Namespace{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: name}, namespace)
	assert.NoError(t, err)
	assert.Equal(t, username, namespace.Labels["owner"])
	assert.Equal(t, tcNamespace.Revision, namespace.Labels["revision"])
}

func createNamespaceWithLabels(t *testing.T, client client.Client, tcNamespace toolchainv1alpha1.Namespace, username string) {
	params := make(map[string]string)
	params["USER_NAME"] = username
	err := applyTemplateTestMock(client, tcNamespace, params)
	require.NoError(t, err)

	name := toNamespaceName(username, tcNamespace.Type)
	namespace := &corev1.Namespace{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: name}, namespace)
	require.NoError(t, err)

	namespace.Labels = make(map[string]string)
	namespace.Labels["owner"] = username
	namespace.Labels["revision"] = tcNamespace.Revision

	err = client.Update(context.TODO(), namespace)
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

// applyTemplate test mock
func applyTemplateTestMock(client client.Client, tcNamespace toolchainv1alpha1.Namespace, params map[string]string) error {
	userName := params["USER_NAME"]
	name := toNamespaceName(userName, tcNamespace.Type)

	// create project
	project := &projectv1.Project{}
	if err := client.Get(context.TODO(), types.NamespacedName{Name: name}, project); err != nil {
		if errors.IsNotFound(err) {
			prjReq := &projectv1.Project{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Status: projectv1.ProjectStatus{
					Phase: corev1.NamespaceActive,
				},
			}
			if err := client.Create(context.TODO(), prjReq); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// create namespace
	namespace := &corev1.Namespace{}
	if err := client.Get(context.TODO(), types.NamespacedName{Name: name}, namespace); err != nil {
		if errors.IsNotFound(err) {
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
				Status: corev1.NamespaceStatus{
					Phase: corev1.NamespaceActive,
				},
			}
			if err := client.Create(context.TODO(), namespace); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	return nil
}
