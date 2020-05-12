package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	authv1 "github.com/openshift/api/authorization/v1"
	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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

	t.Run("status provisioned when cluster resources are missing", func(t *testing.T) {
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

	t.Run("status provisioned with cluster resources", func(t *testing.T) {
		// given
		// create cluster resource quotas
		crq := newClusterResourceQuota(username, "advanced")
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
		codeNS := newNamespace("advanced", username, "code", withRevision("abcde11"))
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withClusterResources())
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

	t.Run("should not create ClusterResource objects when the field is nil but provision namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when
		res, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "code").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", r.client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "dev").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel("toolchain.dev.openshift.com/revision").
			HasNoLabel("toolchain.dev.openshift.com/tier")
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
}

func TestReconcileUpdate(t *testing.T) {

	logf.SetLogger(logf.ZapLogger(true))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("upgrade from basic to advanced tier", func(t *testing.T) {

		t.Run("create ClusterResourceQuota", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev"), withClusterResources())
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("basic", username, "dev", withRevision("abcde11"))
			codeNS := newNamespace("basic", username, "code", withRevision("abcde11"))
			devRo := newRole(devNS.Name, "rbac-edit")
			codeRo := newRole(codeNS.Name, "rbac-edit")
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, codeNS, devRo, codeRo)

			err := fakeClient.Update(context.TODO(), nsTmplSet)
			require.NoError(t, err)

			// when - should create ClusterResourceQuota
			_, err = r.Reconcile(req)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // upgraded
			for _, nsType := range []string{"code", "dev"} {
				AssertThatNamespace(t, username+"-"+nsType, r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/tier", "basic"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/type", nsType).
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("rbac-edit", &rbacv1.Role{})
			}

			t.Run("delete redundant namespace", func(t *testing.T) {

				// when - should delete the -code namespace
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())                             // still in progress
				AssertThatNamespace(t, codeNS.Name, r.client).
					DoesNotExist()                                        // namespace was deleted
				AssertThatNamespace(t, devNS.Name, r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/revision", "abcde11").
					HasLabel("toolchain.dev.openshift.com/type", "dev").
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasLabel("toolchain.dev.openshift.com/tier", "basic") // not upgraded yet

				t.Run("upgrade the dev namespace", func(t *testing.T) {

					// when - should upgrade the namespace
					_, err = r.Reconcile(req)

					// then
					require.NoError(t, err)
					// NSTemplateSet provisioning is complete
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Updating())
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
							WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
					AssertThatNamespace(t, codeNS.Name, r.client).
						DoesNotExist()
					AssertThatNamespace(t, username+"-dev", r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/tier", "advanced").
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain")

					t.Run("when nothing to upgrade, then it should be provisioned", func(t *testing.T) {

						// when - should check if everything is OK and set status to provisioned
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
							HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // not updgraded yet
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
							HasResource("user-edit", &authv1.RoleBinding{}) // role has been removed
					})
				})
			})
		})
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

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

func TestDeleteNSTemplateSet(t *testing.T) {
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("with cluster resources and 2 user namespaces to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "code")
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("dev", "code"), withDeletionTs(), withClusterResources())
		crq := newClusterResourceQuota(username, "advanced")
		devNS := newNamespace("advanced", username, "dev", withRevision("abcde11"))
		codeNS := newNamespace("advanced", username, "code", withRevision("abcde11"))
		r, _ := prepareController(t, nsTmplSet, crq, devNS, codeNS)

		t.Run("reconcile after nstemplateset deletion triggers deletion of the first namespace", func(t *testing.T) {
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

			t.Run("reconcile after first user namespace deletion triggers deletion of the second namespace", func(t *testing.T) {
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

				t.Run("reconcile after second user namespace deletion triggers deletion of CRQ", func(t *testing.T) {
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

					t.Run("reconcile after cluster resource quota deletion triggers removal of the finalizer", func(t *testing.T) {
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
						AssertThatCluster(t, r.client).HasNoResource("for-"+username, &quotav1.ClusterResourceQuota{})

						t.Run("final reconcile after successful deletion", func(t *testing.T) {
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

func prepareApiClient(t *testing.T, initObjs ...runtime.Object) (*apiClient, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()
	fakeClient := test.NewFakeClient(t, initObjs...)

	// objects created from OpenShift templates are `*unstructured.Unstructured`,
	// which causes troubles when calling the `List` method on the fake client,
	// so we're explicitly converting the objects during their creation and update
	fakeClient.MockCreate = func(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		if err := test.Create(fakeClient, ctx, o, opts...); err != nil {
			return err
		}
		return passGeneration(o, obj)
	}
	fakeClient.MockUpdate = func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
		o, err := toStructured(obj, decoder)
		if err != nil {
			return err
		}
		if err := test.Update(fakeClient, ctx, o, opts...); err != nil {
			return err
		}
		return passGeneration(o, obj)
	}
	return &apiClient{
		client:          fakeClient,
		scheme:          s,
		templateContent: newTemplateContentProvider(getTemplateContent(decoder)),
	}, fakeClient
}

func prepareStatusManager(t *testing.T, initObjs ...runtime.Object) (*statusManager, *test.FakeClient) {
	apiClient, fakeClient := prepareApiClient(t, initObjs...)
	return &statusManager{
		apiClient: apiClient,
	}, fakeClient
}

func prepareNamespacesManager(t *testing.T, initObjs ...runtime.Object) (*namespacesManager, *test.FakeClient) {
	statusManager, fakeClient := prepareStatusManager(t, initObjs...)
	return &namespacesManager{
		statusManager: statusManager,
	}, fakeClient
}

func prepareClusterResourcesManager(t *testing.T, initObjs ...runtime.Object) (*clusterResourcesManager, *test.FakeClient) {
	statusManager, fakeClient := prepareStatusManager(t, initObjs...)
	return &clusterResourcesManager{
		statusManager: statusManager,
	}, fakeClient
}

func prepareController(t *testing.T, initObjs ...runtime.Object) (*NSTemplateSetReconciler, *test.FakeClient) {
	apiClient, fakeClient := prepareApiClient(t, initObjs...)
	return newReconciler(apiClient), fakeClient
}

func passGeneration(from, to runtime.Object) error {
	fromMeta, err := meta.Accessor(from)
	if err != nil {
		return err
	}
	toMeta, err := meta.Accessor(to)
	if err != nil {
		return err
	}
	toMeta.SetGeneration(fromMeta.GetGeneration())
	return nil
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

func newRole(namespace, name string) *rbacv1.Role { //nolint: unparam
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
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
			Generation:  int64(1),
		},
		Spec: quotav1.ClusterResourceQuotaSpec{
			Quota: corev1.ResourceQuotaSpec{
				Hard: map[corev1.ResourceName]resource.Quantity{
					"limits.cpu":    resource.MustParse("2000m"),
					"limits.memory": resource.MustParse("10Gi"),
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
			return nil, fmt.Errorf("failed to retrieve template")
		}
		var tmplContent string
		switch tierName {
		case "advanced": // assume that this tier has a "cluster resources" template
			switch typeName {
			case ClusterResources:
				tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq), test.WithParams(username))
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role, rbacRb), test.WithParams(username))
			}
		case "basic":
			switch typeName {
			case ClusterResources: // assume that this tier has no "cluster resources" template
				return nil, nil
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb), test.WithParams(username))
			}
		case "team": // assume that this tier has a "cluster resources" template
			switch typeName {
			case ClusterResources:
				tmplContent = test.CreateTemplate(test.WithObjects(teamCrq), test.WithParams(username))
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role, rbacRb), test.WithParams(username))
			}
		case "withemptycrq":
			switch typeName {
			case ClusterResources:
				tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq, emptyCrq), test.WithParams(username))
			default:
				tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role), test.WithParams(username))
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

	rbacRb test.TemplateObject = `
- apiVersion: authorization.openshift.io/v1
  kind: RoleBinding
  metadata:
    name: user-rbac-edit
    namespace: ${USERNAME}-nsType
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: Role
    name: rbac-edit
  subjects:
    - kind: User
      name: ${USERNAME}}`

	role test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: rbac-edit
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
        limits.cpu: 2000m
        limits.memory: 10Gi
    selector:
      annotations:
        openshift.io/requester: ${USERNAME}
    labels: null
  `
	teamCrq test.TemplateObject = `
- apiVersion: quota.openshift.io/v1
  kind: ClusterResourceQuota
  metadata:
    name: for-${USERNAME}
  spec:
    quota:
      hard:
        limits.cpu: 4000m
        limits.memory: 15Gi
    selector:
      annotations:
        openshift.io/requester: ${USERNAME}
    labels: null
  `

	emptyCrq test.TemplateObject = `
- apiVersion: quota.openshift.io/v1
  kind: ClusterResourceQuota
  metadata:
    name: for-empty
  spec:
`
)
