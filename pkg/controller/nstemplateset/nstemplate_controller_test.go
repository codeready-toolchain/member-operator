package nstemplateset

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	projectv1 "github.com/openshift/api/project/v1"
	authv1 "github.com/openshift/api/authorization/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestReconcile(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))

	t.Run("create projects", func(t *testing.T) {
		// given
		namespace := "foo"
		name := "bar"
		r, req, client := prepareReconcile(t, namespace, name)
		// also, create the NSTemplateSet CRwith the client
		tmplSet := &toolchainv1alpha1.NSTemplateSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
			},
		}
		client.Create(context.TODO(), tmplSet)

		// when
		result, err := r.Reconcile(req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, result)
		// check that the project request was created
		err = r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: "", // project request is cluster-scoped
			Name:      "foo",
		}, &projectv1.ProjectRequest{})
		require.NoError(t, err)
		// check that the rolebinding was created in the namespace
		// (the fake client just records the request but does not perform any consistency check)
		err = r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: namespace, 
			Name:      "user-admin",
		}, &authv1.RoleBinding{})
		require.NoError(t, err)
	})

}

func newReconcileRequest(namespace, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func prepareReconcile(t *testing.T, namespace, name string, initObjs ...runtime.Object) (*ReconcileNSTemplateSet, reconcile.Request, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	client := test.NewFakeClient(t, initObjs...)

	r := &ReconcileNSTemplateSet{
		client: client,
		scheme: s,
	}
	return r, newReconcileRequest(namespace, name), client
}
