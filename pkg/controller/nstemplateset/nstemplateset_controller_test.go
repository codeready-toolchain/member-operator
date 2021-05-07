package nstemplateset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	. "github.com/codeready-toolchain/member-operator/test"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileAddFinalizer(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
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

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"
	t.Run("status provisioned when cluster resources are missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
		codeNS := newNamespace("basic", username, "code", withTemplateRefUsingRevision("abcde11"))
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
			HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
			HasLabel("toolchain.dev.openshift.com/tier", "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
		AssertThatNamespace(t, username+"-code", fakeClient).
			HasLabel("toolchain.dev.openshift.com/owner", username).
			HasLabel("toolchain.dev.openshift.com/type", "code").
			HasLabel("toolchain.dev.openshift.com/templateref", "basic-code-abcde11").
			HasLabel("toolchain.dev.openshift.com/tier", "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
	})

	t.Run("status provisioned with cluster resources", func(t *testing.T) {
		// given
		// create cluster resources
		crq := newClusterResourceQuota(username, "advanced")
		crb := newTektonClusterRoleBinding(username, "advanced")
		idlerDev := newIdler(username, username+"-dev")
		idlerCode := newIdler(username, username+"-code")
		idlerStage := newIdler(username, username+"-stage")
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
		codeNS := newNamespace("advanced", username, "code", withTemplateRefUsingRevision("abcde11"))
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev", "code"), withClusterResources("abcde11"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, crq, devNS, codeNS, crb, idlerDev, idlerCode, idlerStage)

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
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev", "code"))
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
			HasNoLabel("toolchain.dev.openshift.com/templateref").
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

func TestProvisionTwoUsers(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "john"
	namespaceName := "toolchain-member"

	t.Run("provision john's ClusterResourceQuota first", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when
		res, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasConditions(Provisioning())
		AssertThatNamespace(t, username+"-dev", fakeClient).
			DoesNotExist()
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
			HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("provision john's clusterRoleBinding", func(t *testing.T) {
			// when
			res, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev").
				HasConditions(Provisioning())
			AssertThatNamespace(t, username+"-dev", fakeClient).
				DoesNotExist()
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			t.Run("provision john's Idlers", func(t *testing.T) {
				// when
				res, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasSpecNamespaces("dev").
					HasConditions(Provisioning())
				AssertThatNamespace(t, username+"-dev", fakeClient).
					DoesNotExist()
				AssertThatCluster(t, fakeClient).
					HasResource(username+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

				// when
				res, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasConditions(Provisioning())
				AssertThatCluster(t, fakeClient).
					HasResource(username+"-code", &toolchainv1alpha1.Idler{})

				// when
				res, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasConditions(Provisioning())
				AssertThatCluster(t, fakeClient).
					HasResource(username+"-stage", &toolchainv1alpha1.Idler{})

				t.Run("provision john's dev namespace", func(t *testing.T) {
					// when
					res, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					assert.Equal(t, reconcile.Result{}, res)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasSpecNamespaces("dev").
						HasConditions(Provisioning())
					AssertThatNamespace(t, username+"-dev", fakeClient).
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasNoLabel("toolchain.dev.openshift.com/templateref").
						HasNoLabel("toolchain.dev.openshift.com/tier").
						HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
						HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

					t.Run("provision john's inner resources of dev namespace", func(t *testing.T) {
						// given - when host cluster is not ready, then it should use the cache
						r.getHostCluster = NewGetHostCluster(fakeClient, true, v1.ConditionFalse)

						// when
						res, err := r.Reconcile(req)

						// then
						require.NoError(t, err)
						assert.Equal(t, reconcile.Result{}, res)
						AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
							HasFinalizer().
							HasSpecNamespaces("dev").
							HasConditions(Provisioning())
						AssertThatNamespace(t, username+"-dev", fakeClient).
							HasLabel("toolchain.dev.openshift.com/owner", username).
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
							HasLabel("toolchain.dev.openshift.com/tier", "advanced").
							HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
							HasResource("user-edit", &rbacv1.RoleBinding{})
						AssertThatCluster(t, fakeClient).
							HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
							HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

						t.Run("provision ClusterResourceQuota for the joe user (using cached TierTemplate)", func(t *testing.T) {
							// given
							joeUsername := "joe"
							joeNsTmplSet := newNSTmplSet(namespaceName, joeUsername, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
							err := fakeClient.Create(context.TODO(), joeNsTmplSet)
							require.NoError(t, err)
							joeReq := newReconcileRequest(namespaceName, joeUsername)

							// when
							res, err := r.Reconcile(joeReq)

							// then
							require.NoError(t, err)
							assert.Equal(t, reconcile.Result{}, res)
							AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
								HasFinalizer().
								HasSpecNamespaces("dev").
								HasConditions(Provisioning())
							AssertThatNamespace(t, joeUsername+"-dev", fakeClient).
								DoesNotExist()
							AssertThatCluster(t, fakeClient).
								HasResource("for-"+joeUsername, &quotav1.ClusterResourceQuota{}).
								HasNoResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})

							t.Run("provision joe's clusterRoleBinding (using cached TierTemplate)", func(t *testing.T) {
								// when
								res, err := r.Reconcile(joeReq)

								// then
								require.NoError(t, err)
								assert.Equal(t, reconcile.Result{}, res)
								AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
									HasFinalizer().
									HasSpecNamespaces("dev").
									HasConditions(Provisioning())
								AssertThatNamespace(t, joeUsername+"-dev", fakeClient).
									DoesNotExist()
								AssertThatCluster(t, fakeClient).
									HasResource("for-"+joeUsername, &quotav1.ClusterResourceQuota{}).
									HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})

								t.Run("provision joe's Idlers", func(t *testing.T) {
									// when
									res, err := r.Reconcile(joeReq)

									// then
									require.NoError(t, err)
									assert.Equal(t, reconcile.Result{}, res)
									AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
										HasFinalizer().
										HasSpecNamespaces("dev").
										HasConditions(Provisioning())
									AssertThatNamespace(t, joeUsername+"-dev", fakeClient).
										DoesNotExist()
									AssertThatCluster(t, fakeClient).
										HasResource(joeUsername+"-dev", &toolchainv1alpha1.Idler{}).
										HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})

									// when
									res, err = r.Reconcile(joeReq)

									// then
									require.NoError(t, err)
									assert.Equal(t, reconcile.Result{}, res)
									AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
										HasConditions(Provisioning())
									AssertThatCluster(t, fakeClient).
										HasResource(joeUsername+"-code", &toolchainv1alpha1.Idler{})

									// when
									res, err = r.Reconcile(joeReq)

									// then
									require.NoError(t, err)
									assert.Equal(t, reconcile.Result{}, res)
									AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
										HasConditions(Provisioning())
									AssertThatCluster(t, fakeClient).
										HasResource(joeUsername+"-stage", &toolchainv1alpha1.Idler{})

									t.Run("provision joe's dev namespace (using cached TierTemplate)", func(t *testing.T) {
										// when
										res, err := r.Reconcile(joeReq)

										// then
										require.NoError(t, err)
										assert.Equal(t, reconcile.Result{}, res)
										AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
											HasFinalizer().
											HasSpecNamespaces("dev").
											HasConditions(Provisioning())
										AssertThatNamespace(t, joeUsername+"-dev", fakeClient).
											HasLabel("toolchain.dev.openshift.com/owner", joeUsername).
											HasLabel("toolchain.dev.openshift.com/type", "dev").
											HasNoLabel("toolchain.dev.openshift.com/templateref").
											HasNoLabel("toolchain.dev.openshift.com/tier").
											HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
										AssertThatCluster(t, fakeClient).
											HasResource("for-"+username, &quotav1.ClusterResourceQuota{}).
											HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})

										t.Run("provision inner resources of joe's dev namespace (using cached TierTemplate)", func(t *testing.T) {
											// given - when host cluster is not ready, then it should use the cache
											r.getHostCluster = NewGetHostCluster(fakeClient, true, v1.ConditionFalse)

											// when
											res, err := r.Reconcile(joeReq)

											// then
											require.NoError(t, err)
											assert.Equal(t, reconcile.Result{}, res)
											AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
												HasFinalizer().
												HasSpecNamespaces("dev").
												HasConditions(Provisioning())
											AssertThatNamespace(t, joeUsername+"-dev", fakeClient).
												HasLabel("toolchain.dev.openshift.com/owner", joeUsername).
												HasLabel("toolchain.dev.openshift.com/type", "dev").
												HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
												HasLabel("toolchain.dev.openshift.com/tier", "advanced").
												HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
												HasResource("user-edit", &rbacv1.RoleBinding{})
											AssertThatCluster(t, fakeClient).
												HasResource("for-"+joeUsername, &quotav1.ClusterResourceQuota{}).
												HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})
										})
									})
								})
							})
						})
					})
				})
			})
		})
	})
}

func TestReconcilePromotion(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("upgrade from basic to advanced tier", func(t *testing.T) {

		t.Run("create ClusterResourceQuota", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("basic", username, "dev", withTemplateRefUsingRevision("abcde11"))
			codeNS := newNamespace("basic", username, "code", withTemplateRefUsingRevision("abcde11"))
			devRo := newRole(devNS.Name, "rbac-edit")
			codeRo := newRole(codeNS.Name, "rbac-edit")
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet, devNS, codeNS, devRo, codeRo)

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
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")). // upgraded
				HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			for _, nsType := range []string{"code", "dev"} {
				AssertThatNamespace(t, username+"-"+nsType, r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/templateref", "basic-"+nsType+"-abcde11"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/tier", "basic").
					HasLabel("toolchain.dev.openshift.com/type", nsType).
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("rbac-edit", &rbacv1.Role{})
			}

			t.Run("create ClusterRoleBinding", func(t *testing.T) {
				// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
				for _, nsType := range []string{"code", "dev"} {
					AssertThatNamespace(t, username+"-"+nsType, r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/templateref", "basic-"+nsType+"-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/tier", "basic"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/type", nsType).
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasResource("rbac-edit", &rbacv1.Role{})
				}

				t.Run("create Idlers", func(t *testing.T) {
					// when
					_, err = r.Reconcile(req)
					// then
					require.NoError(t, err)
					AssertThatCluster(t, fakeClient).
						HasResource(username+"-dev", &toolchainv1alpha1.Idler{})

					// when
					_, err = r.Reconcile(req)
					// then
					require.NoError(t, err)
					AssertThatCluster(t, fakeClient).
						HasResource(username+"-code", &toolchainv1alpha1.Idler{})

					// when
					_, err = r.Reconcile(req)
					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Updating())
					AssertThatCluster(t, fakeClient).
						HasResource(username+"-stage", &toolchainv1alpha1.Idler{},
							WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
							WithLabel("toolchain.dev.openshift.com/tier", "advanced"))

					t.Run("delete redundant namespace", func(t *testing.T) {

						// when - should delete the -code namespace
						_, err := r.Reconcile(req)

						// then
						require.NoError(t, err)
						AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
							HasFinalizer().
							HasConditions(Updating())
						AssertThatCluster(t, fakeClient).
							HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
								WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
								WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
						AssertThatNamespace(t, codeNS.Name, r.client).
							DoesNotExist() // namespace was deleted
						AssertThatNamespace(t, devNS.Name, r.client).
							HasNoOwnerReference().
							HasLabel("toolchain.dev.openshift.com/owner", username).
							HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
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
									WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
									WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
							AssertThatNamespace(t, codeNS.Name, r.client).
								DoesNotExist()
							AssertThatNamespace(t, username+"-dev", r.client).
								HasNoOwnerReference().
								HasLabel("toolchain.dev.openshift.com/owner", username).
								HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
								HasLabel("toolchain.dev.openshift.com/tier", "advanced").
								HasLabel("toolchain.dev.openshift.com/type", "dev").
								HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain")

							t.Run("when nothing to upgrade, then it should be provisioned", func(t *testing.T) {
								// given - when host cluster is not ready, then it should use the cache (for both TierTemplates)
								r.getHostCluster = NewGetHostCluster(fakeClient, true, v1.ConditionFalse)

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
									HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
									HasLabel("toolchain.dev.openshift.com/owner", username).
									HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // not updgraded yet
									HasLabel("toolchain.dev.openshift.com/type", "dev").
									HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
									HasResource("user-edit", &rbacv1.RoleBinding{}) // role has been removed
							})
						})
					})
				})
			})
		})
	})
}

func TestReconcileUpdate(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	username := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("upgrade from abcde11 to abcde12 as part of the advanced tier", func(t *testing.T) {

		t.Run("update ClusterResourceQuota", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde12", "dev"), withClusterResources("abcde12"))

			devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
			devRo := newRole(devNS.Name, "rbac-edit")
			devRb := newRoleBinding(devNS.Name, "user-edit")
			devRbacRb := newRoleBinding(devNS.Name, "user-rbac-edit")

			codeNS := newNamespace("advanced", username, "code", withTemplateRefUsingRevision("abcde11"))
			codeRo := newRole(codeNS.Name, "rbac-edit")
			codeRb := newRoleBinding(codeNS.Name, "user-edit")
			codeRbacRb := newRoleBinding(codeNS.Name, "user-rbac-edit")

			crb := newTektonClusterRoleBinding(username, "advanced")
			crq := newClusterResourceQuota(username, "advanced")
			r, req, fakeClient := prepareReconcile(t, namespaceName, username, nsTmplSet,
				devNS, devRo, devRb, devRbacRb, codeNS, codeRo, codeRb, codeRbacRb, crq, crb)

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
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")). // upgraded
				HasResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))

			for _, nsType := range []string{"code", "dev"} {
				AssertThatNamespace(t, username+"-"+nsType, r.client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", username).
					HasLabel("toolchain.dev.openshift.com/templateref", "advanced-"+nsType+"-abcde11"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/tier", "advanced").
					HasLabel("toolchain.dev.openshift.com/type", nsType).
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("user-edit", &rbacv1.RoleBinding{}).
					HasResource("rbac-edit", &rbacv1.Role{}).
					HasResource("user-rbac-edit", &rbacv1.RoleBinding{})
			}

			t.Run("delete ClusterRoleBinding", func(t *testing.T) {
				// when
				_, err = r.Reconcile(req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
														WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
														WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{}) // deleted
				for _, nsType := range []string{"code", "dev"} {
					AssertThatNamespace(t, username+"-"+nsType, r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/templateref", "advanced-"+nsType+"-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/tier", "advanced").
						HasLabel("toolchain.dev.openshift.com/type", nsType).
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasResource("user-edit", &rbacv1.RoleBinding{}).
						HasResource("rbac-edit", &rbacv1.Role{}).
						HasResource("user-rbac-edit", &rbacv1.RoleBinding{})
				}

				t.Run("delete redundant namespace", func(t *testing.T) {

					// when - should delete the -code namespace
					_, err := r.Reconcile(req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, username, fakeClient).
						HasFinalizer().
						HasConditions(Updating())
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+username, &quotav1.ClusterResourceQuota{},
							WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
							WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
						HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
					AssertThatNamespace(t, codeNS.Name, r.client).
						DoesNotExist() // namespace was deleted
					AssertThatNamespace(t, devNS.Name, r.client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", username).
						HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasLabel("toolchain.dev.openshift.com/tier", "advanced").
						HasResource("user-edit", &rbacv1.RoleBinding{}).
						HasResource("rbac-edit", &rbacv1.Role{}).
						HasResource("user-rbac-edit", &rbacv1.RoleBinding{})

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
								WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
								WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
							HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
						AssertThatNamespace(t, codeNS.Name, r.client).
							DoesNotExist()
						AssertThatNamespace(t, devNS.Name, r.client).
							HasNoOwnerReference().
							HasLabel("toolchain.dev.openshift.com/owner", username).
							HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde12"). // upgraded
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
							HasLabel("toolchain.dev.openshift.com/tier", "advanced").
							HasResource("user-edit", &rbacv1.RoleBinding{}).
							HasResource("rbac-edit", &rbacv1.Role{}).
							HasNoResource("user-rbac-edit", &rbacv1.RoleBinding{})

						t.Run("when nothing to update, then it should be provisioned", func(t *testing.T) {
							// given - when host cluster is not ready, then it should use the cache (for both TierTemplates)
							r.getHostCluster = NewGetHostCluster(fakeClient, true, v1.ConditionFalse)

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
									WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
									WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
								HasNoResource(username+"-tekton-view", &rbacv1.ClusterRoleBinding{})
							AssertThatNamespace(t, codeNS.Name, r.client).
								DoesNotExist()
							AssertThatNamespace(t, devNS.Name, r.client).
								HasNoOwnerReference().
								HasLabel("toolchain.dev.openshift.com/owner", username).
								HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde12"). // upgraded
								HasLabel("toolchain.dev.openshift.com/type", "dev").
								HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
								HasLabel("toolchain.dev.openshift.com/tier", "advanced").
								HasResource("user-edit", &rbacv1.RoleBinding{}).
								HasResource("rbac-edit", &rbacv1.Role{}).
								HasNoResource("user-rbac-edit", &rbacv1.RoleBinding{})
						})
					})
				})
			})
		})
	})
}

func TestReconcileProvisionFail(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

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
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withNamespaces("abcde11", "dev", "code"))
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
		nsTmplSet := newNSTmplSet(namespaceName, username, "advanced", withNamespaces("abcde11", "dev", "code"), withDeletionTs(), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(username, "advanced")
		devNS := newNamespace("advanced", username, "dev", withTemplateRefUsingRevision("abcde11"))
		codeNS := newNamespace("advanced", username, "code", withTemplateRefUsingRevision("abcde11"))
		r, _ := prepareController(t, nsTmplSet, crq, devNS, codeNS)
		req := newReconcileRequest(namespaceName, username)

		t.Run("reconcile after nstemplateset deletion triggers deletion of the first namespace", func(t *testing.T) {
			// when a first reconcile loop was triggered (because a cluster resource quota was deleted)
			_, err := r.Reconcile(req)

			// then
			require.NoError(t, err)
			// get the first namespace and check its deletion timestamp
			firstNSName := fmt.Sprintf("%s-dev", username)
			AssertThatNamespace(t, firstNSName, r.client).DoesNotExist()
			// get the NSTemplateSet resource again and check its status
			AssertThatNSTemplateSet(t, namespaceName, username, r.client).
				HasFinalizer(). // the finalizer should NOT have been removed yet
				HasConditions(Terminating())

			t.Run("reconcile after first user namespace deletion triggers deletion of the second namespace", func(t *testing.T) {
				// when a second reconcile loop was triggered (because a user namespace was deleted)
				_, err := r.Reconcile(req)

				// then
				require.NoError(t, err)
				// get the second namespace and check its deletion timestamp
				secondtNSName := fmt.Sprintf("%s-code", username)
				AssertThatNamespace(t, secondtNSName, r.client).DoesNotExist()
				// get the NSTemplateSet resource again and check its finalizers and status
				AssertThatNSTemplateSet(t, namespaceName, username, r.client).
					HasFinalizer(). // the finalizer should not have been removed either
					HasConditions(Terminating())

				t.Run("reconcile after second user namespace deletion triggers deletion of CRQ", func(t *testing.T) {
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
						// given - when host cluster is not ready, then it should use the cache
						r.getHostCluster = NewGetHostCluster(r.client, true, v1.ConditionFalse)

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
		nsTmplSet := newNSTmplSet(namespaceName, username, "basic", withoutFinalizer(), withDeletionTs(), withClusterResources("abcde11"), withNamespaces("abcde11", "dev", "code"))
		r, req, _ := prepareReconcile(t, namespaceName, username, nsTmplSet)

		// when a reconcile loop is triggered
		_, err := r.Reconcile(req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, username, r.client).
			DoesNotHaveFinalizer() // finalizer was not added and nothing else was done
	})
}

func prepareReconcile(t *testing.T, namespaceName, name string, initObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient) {
	r, fakeClient := prepareController(t, initObjs...)
	return r, newReconcileRequest(namespaceName, name), fakeClient
}

func prepareAPIClient(t *testing.T, initObjs ...runtime.Object) (*apiClient, *test.FakeClient) {
	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)
	codecFactory := serializer.NewCodecFactory(s)
	decoder := codecFactory.UniversalDeserializer()
	tierTemplates, err := getTemplateTiers(decoder)
	require.NoError(t, err)
	fakeClient := test.NewFakeClient(t, append(initObjs, tierTemplates...)...)
	resetCache()

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
		client:         fakeClient,
		scheme:         s,
		getHostCluster: NewGetHostCluster(fakeClient, true, v1.ConditionTrue),
	}, fakeClient
}

func prepareStatusManager(t *testing.T, initObjs ...runtime.Object) (*statusManager, *test.FakeClient) {
	apiClient, fakeClient := prepareAPIClient(t, initObjs...)
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

func prepareController(t *testing.T, initObjs ...runtime.Object) (*Reconciler, *test.FakeClient) {
	apiClient, fakeClient := prepareAPIClient(t, initObjs...)
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
		case "ClusterRoleBinding":
			crb := &rbacv1.ClusterRoleBinding{}
			_, _, err = decoder.Decode(data, nil, crb)
			return crb, err
		case "Namespace":
			ns := &v1.Namespace{}
			_, _, err = decoder.Decode(data, nil, ns)
			ns.Status = corev1.NamespaceStatus{Phase: corev1.NamespaceActive}
			return ns, err
		case "Idler":
			idler := &toolchainv1alpha1.Idler{}
			_, _, err = decoder.Decode(data, nil, idler)
			return idler, err
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
		deletionTS := metav1.Now()
		nsTmplSet.SetDeletionTimestamp(&deletionTS)
	}
}

func withNamespaces(revision string, types ...string) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nss := make([]toolchainv1alpha1.NSTemplateSetNamespace, len(types))
		for index, nsType := range types {
			nss[index] = toolchainv1alpha1.NSTemplateSetNamespace{
				TemplateRef: NewTierTemplateName(nsTmplSet.Spec.TierName, nsType, revision),
			}
		}
		nsTmplSet.Spec.Namespaces = nss
	}
}

func withClusterResources(revision string) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Spec.ClusterResources = &toolchainv1alpha1.NSTemplateSetClusterResources{
			TemplateRef: NewTierTemplateName(nsTmplSet.Spec.TierName, "clusterresources", revision),
		}
	}
}

func withConditions(conditions ...toolchainv1alpha1.Condition) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Status.Conditions = conditions
	}
}

func newNamespace(tier, username, typeName string, options ...objectMetaOption) *corev1.Namespace {
	labels := map[string]string{
		"toolchain.dev.openshift.com/owner":    username,
		"toolchain.dev.openshift.com/type":     typeName,
		"toolchain.dev.openshift.com/provider": "codeready-toolchain",
	}
	if tier != "" {
		labels["toolchain.dev.openshift.com/templateref"] = NewTierTemplateName(tier, typeName, "abcde11")
		labels["toolchain.dev.openshift.com/tier"] = tier
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("%s-%s", username, typeName),
			Labels: labels,
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	for _, set := range options {
		ns.ObjectMeta = set(ns.ObjectMeta, tier, typeName)
	}
	return ns
}

func newRoleBinding(namespace, name string) *rbacv1.RoleBinding { //nolint: unparam
	return &rbacv1.RoleBinding{
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

func newTektonClusterRoleBinding(username, tier string) *rbacv1.ClusterRoleBinding {
	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterRoleBinding",
			APIVersion: rbacv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider":    "codeready-toolchain",
				"toolchain.dev.openshift.com/tier":        tier,
				"toolchain.dev.openshift.com/templateref": NewTierTemplateName(tier, "clusterresources", "abcde11"),
				"toolchain.dev.openshift.com/owner":       username,
				"toolchain.dev.openshift.com/type":        "clusterresources",
			},
			Name:       username + "-tekton-view",
			Generation: int64(1),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "tekton-view-for-" + username,
		},
		Subjects: []rbacv1.Subject{{
			Kind: "User",
			Name: username,
		}},
	}
	return crb
}

func newClusterResourceQuota(username, tier string, options ...objectMetaOption) *quotav1.ClusterResourceQuota {
	crq := &quotav1.ClusterResourceQuota{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterResourceQuota",
			APIVersion: quotav1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider":    "codeready-toolchain",
				"toolchain.dev.openshift.com/tier":        tier,
				"toolchain.dev.openshift.com/templateref": NewTierTemplateName(tier, "clusterresources", "abcde11"),
				"toolchain.dev.openshift.com/owner":       username,
				"toolchain.dev.openshift.com/type":        "clusterresources",
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
	for _, option := range options {
		crq.ObjectMeta = option(crq.ObjectMeta, tier, "clusterresources")
	}
	return crq
}

func newIdler(username, name string) *toolchainv1alpha1.Idler {
	idler := &toolchainv1alpha1.Idler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Idler",
			APIVersion: toolchainv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider":    "codeready-toolchain",
				"toolchain.dev.openshift.com/tier":        "basic",
				"toolchain.dev.openshift.com/templateref": NewTierTemplateName("advanced", "clusterresources", "abcde11"),
				"toolchain.dev.openshift.com/owner":       username,
				"toolchain.dev.openshift.com/type":        "clusterresources",
			},
			Name:       name,
			Generation: int64(1),
		},
		Spec: toolchainv1alpha1.IdlerSpec{
			TimeoutSeconds: 30,
		},
	}
	return idler
}

type objectMetaOption func(meta metav1.ObjectMeta, tier, typeName string) metav1.ObjectMeta

func withTemplateRefUsingRevision(revision string) objectMetaOption {
	return func(meta metav1.ObjectMeta, tier, typeName string) metav1.ObjectMeta {
		meta.Labels["toolchain.dev.openshift.com/templateref"] = NewTierTemplateName(tier, typeName, revision)
		return meta
	}
}

func getTemplateTiers(decoder runtime.Decoder) ([]runtime.Object, error) {
	var tierTemplates []runtime.Object

	for _, tierName := range []string{"advanced", "basic", "team", "withemptycrq"} {
		for _, revision := range []string{"abcde11", "abcde12"} {
			for _, typeName := range []string{"code", "dev", "stage", "clusterresources"} {
				var tmplContent string
				switch tierName {
				case "advanced": // assume that this tier has a "cluster resources" template
					switch typeName {
					case "clusterresources":
						if revision == "abcde11" {
							tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq, clusterTektonRb, idlerDev, idlerCode, idlerStage), test.WithParams(username))
						} else {
							tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq), test.WithParams(username))
						}
					default:
						if revision == "abcde11" {
							tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role, rbacRb), test.WithParams(username))
						} else {
							tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role), test.WithParams(username))
						}
					}
				case "basic":
					switch typeName {
					case "clusterresources": // assume that this tier has no "cluster resources" template
						continue
					default:
						tmplContent = test.CreateTemplate(test.WithObjects(ns, rb), test.WithParams(username))
					}
				case "team": // assume that this tier has a "cluster resources" template
					switch typeName {
					case "clusterresources":
						tmplContent = test.CreateTemplate(test.WithObjects(teamCrq, clusterTektonRb), test.WithParams(username))
					default:
						tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role, rbacRb), test.WithParams(username))
					}
				case "withemptycrq":
					switch typeName {
					case "clusterresources":
						tmplContent = test.CreateTemplate(test.WithObjects(advancedCrq, emptyCrq, clusterTektonRb), test.WithParams(username))
					default:
						tmplContent = test.CreateTemplate(test.WithObjects(ns, rb, role), test.WithParams(username))
					}
				}
				tmplContent = strings.ReplaceAll(tmplContent, "nsType", typeName)
				tmpl := templatev1.Template{}
				_, _, err := decoder.Decode([]byte(tmplContent), nil, &tmpl)
				if err != nil {
					return nil, err
				}
				tierTmpl := &toolchainv1alpha1.TierTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      strings.ToLower(fmt.Sprintf("%s-%s-%s", tierName, typeName, revision)),
						Namespace: test.HostOperatorNs,
					},
					Spec: toolchainv1alpha1.TierTemplateSpec{
						TierName: tierName,
						Type:     typeName,
						Revision: revision,
						Template: tmpl,
					},
				}
				tierTemplates = append(tierTemplates, tierTmpl)
			}
		}
	}
	return tierTemplates, nil
}

var (
	ns test.TemplateObject = `
- apiVersion: v1
  kind: Namespace
  metadata:
    name: ${USERNAME}-nsType
`
	rb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
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
- apiVersion: rbac.authorization.k8s.io/v1
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

	clusterTektonRb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRoleBinding
  metadata:
    name: ${USERNAME}-tekton-view
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: tekton-view-for-${USERNAME}
  subjects:
    - kind: User
      name: ${USERNAME}
`
	idlerDev test.TemplateObject = `
- apiVersion: toolchain.dev.openshift.com/v1alpha1
  kind: Idler
  metadata:
    name: ${USERNAME}-dev
  spec:
    timeoutSeconds: 28800 # 8 hours
  `
	idlerCode test.TemplateObject = `
- apiVersion: toolchain.dev.openshift.com/v1alpha1
  kind: Idler
  metadata:
    name: ${USERNAME}-code
  spec:
    timeoutSeconds: 28800 # 8 hours
  `
	idlerStage test.TemplateObject = `
- apiVersion: toolchain.dev.openshift.com/v1alpha1
  kind: Idler
  metadata:
    name: ${USERNAME}-stage
  spec:
    timeoutSeconds: 28800 # 8 hours
  `
)
