package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/codeready-toolchain/toolchain-common/pkg/condition"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
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

const (
	username      = "johnsmith"
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
				Name: "johnsmith-dev", Labels: map[string]string{"owner": "johnsmith", "revision": "abcde11", "type": "dev"},
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
	tcNamespaces := []toolchainv1alpha1.NSTemplateSetNamespace{
		{Type: "dev", Revision: "abcde11"},
		{Type: "code", Revision: "abcde21"},
		{Type: "stage", Revision: "abcde31"},
	}

	t.Run("revision_not_set", func(t *testing.T) {
		// test
		tcNS, userNS, found := nextNamespaceToProvision(tcNamespaces, userNamespaces)

		assert.True(t, found)
		assert.Equal(t, "code", tcNS.Type)
		assert.Equal(t, "johnsmith-code", userNS.GetName())
	})

	t.Run("missing_namespace", func(t *testing.T) {
		userNamespaces[1].Labels["revision"] = "abcde11"

		// test
		tcNS, userNS, found := nextNamespaceToProvision(tcNamespaces, userNamespaces)

		assert.True(t, found)
		assert.Equal(t, "stage", tcNS.Type)
		assert.Nil(t, userNS)
	})

	t.Run("namespace_not_found", func(t *testing.T) {
		userNamespaces[1].Labels["revision"] = "abcde11"
		userNamespaces = append(userNamespaces, corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "johnsmith-stage", Labels: map[string]string{"revision": "abcde11", "type": "stage"},
			},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		})

		// test
		_, _, found := nextNamespaceToProvision(tcNamespaces, userNamespaces)

		assert.False(t, found)
	})
}

func TestGetNamespaceName(t *testing.T) {
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

	nsTmplSet := newNSTmplSet()

	reconcile := func(r *ReconcileNSTemplateSet, req reconcile.Request) {
		res, err := r.Reconcile(req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	}

	t.Run("new_namespace_created_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, r.client, username, "dev")
	})

	t.Run("new_namespace_created_with_existing_namespace_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		// create dev
		createNamespace(t, fakeClient, "abcde11", "dev")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, "Provisioning")
		checkNamespace(t, r.client, username, "code")
	})

	t.Run("inner_resources_created_for_existing_namespace_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		// create dev
		namespace := createNamespace(t, fakeClient, "", "dev")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionFalse, "Provisioning")
		checkInnerResources(t, fakeClient, namespace.GetName())
	})

	t.Run("status_provisioned_ok", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		// create namesapces
		createNamespace(t, fakeClient, "abcde11", "dev")
		createNamespace(t, fakeClient, "abcde21", "code")

		// test
		reconcile(r, req)

		checkReadyCond(t, fakeClient, corev1.ConditionTrue, "Provisioned")
	})

	t.Run("nstmplset_not_found", func(t *testing.T) {
		r, req, _ := prepareReconcile(t)

		// test
		reconcile(r, req)
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	nsTmplSet := newNSTmplSet()

	reconcile := func(r *ReconcileNSTemplateSet, req reconcile.Request, errMsg string) {
		res, err := r.Reconcile(req)
		require.Error(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		assert.Contains(t, err.Error(), errMsg)
	}

	t.Run("fail_create_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)
		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to create namespace")
		}

		// test
		reconcile(r, req, "unable to create namespace")

		checkStatus(t, fakeClient, "UnableToProvisionNamespace")
	})

	t.Run("fail_create_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		createNamespace(t, fakeClient, "", "dev")

		fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to create some object")
		}

		// test
		reconcile(r, req, "unable to create some object")

		checkStatus(t, fakeClient, "UnableToProvisionNamespace")
	})

	t.Run("fail_update_status_for_inner_resources", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)

		createNamespace(t, fakeClient, "", "dev")

		fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object) error {
			return errors.New("unable to update NSTmlpSet")
		}

		// test
		reconcile(r, req, "unable to update NSTmlpSet")

		checkStatus(t, fakeClient, "UnableToProvisionNamespace")
	})

	t.Run("fail_list_namespace", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)
		fakeClient.MockList = func(ctx context.Context, opts *client.ListOptions, list runtime.Object) error {
			return errors.New("unable to list namespace")
		}

		// test
		reconcile(r, req, "unable to list namespace")

		checkStatus(t, fakeClient, "UnableToProvision")
	})

	t.Run("fail_get_nstmplset", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
			return errors.New("unable to get NSTemplate")
		}

		// test
		reconcile(r, req, "unable to get NSTemplate")
	})

	t.Run("fail_status_provisioning", func(t *testing.T) {
		r, req, fakeClient := prepareReconcile(t, nsTmplSet)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj runtime.Object) error {
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
					{Type: "stage", Revision: "abcde31", Template: ""},
				},
			},
		}
		r, req, fakeClient := prepareReconcile(t, nsTmplSetObj)

		// test
		reconcile(r, req, "failed to to retrieve template for namespace")

		checkStatus(t, fakeClient, "UnableToProvisionNamespace")
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
					{Type: "stage", Revision: "abcde31", Template: ""},
				},
			},
		}
		r, req, fakeClient := prepareReconcile(t, nsTmplSetObj)

		createNamespace(t, fakeClient, "", "stage")

		// test
		reconcile(r, req, "failed to to retrieve template for namespace")

		checkStatus(t, fakeClient, "UnableToProvisionNamespace")
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

	t.Run("status_updated", func(t *testing.T) {
		nsTmplSet := newNSTmplSet()
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
		nsTmplSet := newNSTmplSet()
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
		nsTmplSet := newNSTmplSet()
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
		Reason:  unableToProvisionNamespaceReason,
		Message: "Operation cannot be fulfilled on namespaces bla bla bla",
	}
	provisionedCond := toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: provisionedReason,
	}

	t.Run("when status is set to false with message, then next update to true should remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet()
		nsTmplSet.Status.Conditions = []toolchainv1alpha1.Condition{failedCond}
		reconciler, _ := prepareController(t, nsTmplSet)

		// when
		err := reconciler.setStatusReady(nsTmplSet)

		// then
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = reconciler.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions, provisionedCond)
	})

	t.Run("when status is set to false with message, then next successful reconcile should update it to true and remove the message", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet()
		nsTmplSet.Status.Conditions = []toolchainv1alpha1.Condition{failedCond}
		r, req, _ := prepareReconcile(t, nsTmplSet)
		createNamespace(t, r.client, "abcde11", "dev")
		createNamespace(t, r.client, "abcde21", "code")

		// when
		_, err := r.Reconcile(req)

		// then
		updatedNSTmplSet := &toolchainv1alpha1.NSTemplateSet{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: namespaceName, Name: username}, updatedNSTmplSet)
		require.NoError(t, err)
		test.AssertConditionsMatch(t, updatedNSTmplSet.Status.Conditions, provisionedCond)
	})
}

func createNamespace(t *testing.T, client client.Client, revision, typeName string) *corev1.Namespace {
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

func checkReadyCond(t *testing.T, client *test.FakeClient, wantStatus corev1.ConditionStatus, wantReason string) {
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
	require.Equal(t, username, namespace.Labels["owner"])
	require.Equal(t, typeName, namespace.Labels["type"])
}

func checkInnerResources(t *testing.T, client *test.FakeClient, nsName string) {
	t.Helper()

	roleBinding := &authv1.RoleBinding{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "user-edit", Namespace: nsName}, roleBinding)
	require.NoError(t, err)
}

func checkStatus(t *testing.T, client *test.FakeClient, wantReason string) {
	t.Helper()

	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: username, Namespace: namespaceName}, nsTmplSet)
	require.NoError(t, err)
	readyCond, found := condition.FindConditionByType(nsTmplSet.Status.Conditions, toolchainv1alpha1.ConditionReady)
	assert.True(t, found)
	assert.Equal(t, corev1.ConditionFalse, readyCond.Status)
	assert.Equal(t, wantReason, readyCond.Reason)
}

func newNSTmplSet() *toolchainv1alpha1.NSTemplateSet {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: namespaceName,
		},
		Spec: toolchainv1alpha1.NSTemplateSetSpec{
			TierName: "basic",
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
				{Type: "dev", Revision: "abcde11", Template: ""},
				{Type: "code", Revision: "abcde21", Template: ""},
			},
		},
	}
	return nsTmplSet
}

func prepareReconcile(t *testing.T, initObjs ...runtime.Object) (*ReconcileNSTemplateSet, reconcile.Request, *test.FakeClient) {
	r, fakeClient := prepareController(t, initObjs...)
	return r, newReconcileRequest(username), fakeClient
}

func prepareController(t *testing.T, initObjs ...runtime.Object) (*ReconcileNSTemplateSet, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()

	fakeClient := test.NewFakeClient(t, initObjs...)
	r := &ReconcileNSTemplateSet{
		client:             fakeClient,
		scheme:             s,
		getTemplateContent: wrapTetTemplateContent(decoder),
	}
	return r, fakeClient
}

func newReconcileRequest(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespaceName,
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

func wrapTetTemplateContent(decoder runtime.Decoder) func(tierName, typeName string) (*templatev1.Template, error) {
	return func(tierName, typeName string) (*templatev1.Template, error) {
		tmplFile, err := filepath.Abs(filepath.Join("test-files", fmt.Sprintf("%s-%s.yaml", tierName, typeName)))
		if err != nil {
			return nil, err
		}
		tmplContent, err := ioutil.ReadFile(tmplFile)
		if err != nil {
			return nil, err
		}

		tmpl := &templatev1.Template{}
		_, _, err = decoder.Decode(tmplContent, nil, tmpl)
		if err != nil {
			return nil, err
		}
		return tmpl, err
	}
}
