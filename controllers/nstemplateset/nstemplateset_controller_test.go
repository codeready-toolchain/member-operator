package nstemplateset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	. "github.com/codeready-toolchain/member-operator/test"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileAddFinalizer(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("add a finalizer when missing", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withoutFinalizer())
			r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet)

			// when
			res, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer()
		})

		t.Run("failure", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withoutFinalizer())
			r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet)
			fakeClient.MockUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
				fmt.Printf("updating object of type '%T'\n", obj)
				return fmt.Errorf("mock error")
			}

			// when
			res, err := r.Reconcile(context.TODO(), req)

			// then
			require.Error(t, err)

			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				DoesNotHaveFinalizer()
		})
	})

}

func TestReconcileProvisionOK(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("status provisioned when cluster resources and space roles are missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioned())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasLabel("toolchain.dev.openshift.com/owner", spacename).
			HasLabel("toolchain.dev.openshift.com/type", "dev").
			HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
			HasLabel("toolchain.dev.openshift.com/tier", "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasLabel("toolchain.dev.openshift.com/owner", spacename).
			HasLabel("toolchain.dev.openshift.com/type", "stage").
			HasLabel("toolchain.dev.openshift.com/templateref", "basic-stage-abcde11").
			HasLabel("toolchain.dev.openshift.com/tier", "basic").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)
	})

	t.Run("status provisioned with cluster resources", func(t *testing.T) {
		// given
		// create cluster resources
		crq := newClusterResourceQuota(spacename, "advanced")
		crb := newTektonClusterRoleBinding(spacename, "advanced")
		idlerDev := newIdler(spacename, spacename+"-dev", "advanced")
		idlerStage := newIdler(spacename, spacename+"-stage", "advanced")
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"), withClusterResources("abcde11"))
		devRole := newRole(devNS.Name, "exec-pods", spacename)
		devRb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		devRb2 := newRoleBinding(devNS.Name, "crtadmin-view", spacename)
		stageRole := newRole(stageNS.Name, "exec-pods", spacename)
		stageRb := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		stageRb2 := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet,
			crq, crb, idlerDev, idlerStage,
			devNS, stageNS,
			devRole, devRb, devRb2,
			stageRole, stageRb, stageRb2)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioned())
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{})
	})

	t.Run("status should contain provisioned namespaces", func(t *testing.T) {
		// given
		condition := toolchainv1alpha1.Condition{
			Type:   toolchainv1alpha1.ConditionReady,
			Status: corev1.ConditionTrue,
		}
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "stage", "dev"), withConditions(condition))
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2)

		// when
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasProvisionedNamespaces([]toolchainv1alpha1.SpaceNamespace{
				{
					Name: spacename + "-dev",
					Type: "default", // check that default type is added to first NS in alphabetical order
				},
				{
					Name: spacename + "-stage",
					Type: "", // other namespaces do not have type for now...
				},
			}...).
			HasConditions(Provisioned())
	})

	t.Run("should not create ClusterResource objects when the field is nil but provision namespace", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioning())
		AssertThatNamespace(t, spacename+"-dev", r.Client).
			HasNoOwnerReference().
			HasLabel("toolchain.dev.openshift.com/owner", spacename).
			HasLabel("toolchain.dev.openshift.com/type", "dev").
			HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
			HasNoLabel("toolchain.dev.openshift.com/templateref").
			HasNoLabel("toolchain.dev.openshift.com/tier")
	})

	t.Run("should recreate rolebinding when missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())

		// another reconcile creates the missing rolebinding in dev namespace
		res, err = r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})

		// another reconcile creates the missing rolebinding in stage namespace
		res, err = r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioned())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
	})

	t.Run("should recreate role when missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage")) // no cluster resources here
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(devNS.Name, "crtadmin-view", spacename)
		rb3 := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		rb4 := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2, rb3, rb4)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{})
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{})

		t.Run("create the missing role in dev namespace", func(t *testing.T) {
			// when
			res, err = r.Reconcile(context.TODO(), req)
			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev", "stage").
				HasConditions(Updating())
			AssertThatNamespace(t, spacename+"-dev", fakeClient).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
				HasResource("exec-pods", &rbacv1.Role{}) // created
			AssertThatNamespace(t, spacename+"-stage", fakeClient).
				HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
				HasResource("crtadmin-view", &rbacv1.RoleBinding{})

			t.Run("create the missing role in stage namespace", func(t *testing.T) {
				// when
				res, err = r.Reconcile(context.TODO(), req)
				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasSpecNamespaces("dev", "stage").
					HasConditions(Provisioned()) // done with updating
				AssertThatNamespace(t, spacename+"-dev", fakeClient).
					HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
					HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
					HasResource("exec-pods", &rbacv1.Role{})
				AssertThatNamespace(t, spacename+"-stage", fakeClient).
					HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
					HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
					HasResource("exec-pods", &rbacv1.Role{}) // created
			})
		})
	})

	t.Run("should recreate all spacerole-related rolebindings at once when missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic",
			withNamespaces("abcde11", "dev", "stage"),
			withSpaceRoles(map[string][]string{
				"basic-admin-abcde11": {spacename},
			}))
		// create namespaces (and assume they are complete since they have the expected revision number)
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"), withLastAppliedSpaceRoles(nsTmplSet))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"), withLastAppliedSpaceRoles(nsTmplSet))
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet,
			devNS,
			newRole(devNS.Name, "exec-pods", spacename),
			newRoleBinding(devNS.Name, "crtadmin-pods", spacename),
			newRoleBinding(devNS.Name, "crtadmin-view", spacename),
			newRole(devNS.Name, "space-admin", spacename), // `space-admin` role exists, but `${SPACE_NAME}-space-admin` rolebinding is missing
			stageNS,
			newRole(stageNS.Name, "exec-pods", spacename),
			newRoleBinding(stageNS.Name, "crtadmin-pods", spacename),
			newRoleBinding(stageNS.Name, "crtadmin-view", spacename),
			newRole(stageNS.Name, "space-admin", spacename))

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Provisioned()) // status was NOT changed for this particular use-case
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("exec-pods", &rbacv1.Role{}).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			HasResource("space-admin", &rbacv1.Role{}).
			HasResource(spacename+"-space-admin", &rbacv1.RoleBinding{}) // created
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			HasResource("exec-pods", &rbacv1.Role{}).
			HasResource("space-admin", &rbacv1.Role{}).
			HasResource(spacename+"-space-admin", &rbacv1.RoleBinding{}) // also created
	})

	t.Run("should add owner label to role when missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(devNS.Name, "crtadmin-view", spacename)
		rbCode := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		rb2Code := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)
		ro := newRole(devNS.Name, "exec-pods", spacename)
		delete(ro.ObjectMeta.Labels, toolchainv1alpha1.OwnerLabelKey)
		roCode := newRole(stageNS.Name, "exec-pods", spacename)
		delete(roCode.ObjectMeta.Labels, toolchainv1alpha1.OwnerLabelKey)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2, ro, roCode, rbCode, rb2Code)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			HasResource("exec-pods", &rbacv1.Role{})

		//second reconcile adds owner label to role in stage namespace
		res, err = r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
	})

	t.Run("should add owner label to rolebinding when missing", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rbDev := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rbCode := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		delete(rbDev.ObjectMeta.Labels, toolchainv1alpha1.OwnerLabelKey)
		delete(rbCode.ObjectMeta.Labels, toolchainv1alpha1.OwnerLabelKey)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rbDev, rbCode)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})

		//second reconcile adds owner label to rolebinding in stage namespace
		res, err = r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
	})

	t.Run("should correct the value of owner in label of rolebinding when incorrect", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rbDev := newRoleBinding(devNS.Name, "crtadmin-pods", "wrong-owner")
		rbCode := newRoleBinding(stageNS.Name, "crtadmin-pods", "wrong-owner")
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rbDev, rbCode)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{})

		//second reconcile adds owner label to rolebinding in stage namespace
		res, err = r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			ResourceHasOwnerLabel("crtadmin-pods", &rbacv1.RoleBinding{}, spacename)
	})

	t.Run("should correct the value of owner in label of role when incorrect", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(devNS.Name, "crtadmin-view", spacename)
		rbCode := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		rb2Code := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)
		ro := newRole(devNS.Name, "exec-pods", "wrong-spacename")
		roCode := newRole(stageNS.Name, "exec-pods", "wrong-spacename")
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2, ro, roCode, rbCode, rb2Code)

		// when
		res, err := r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			HasResource("exec-pods", &rbacv1.Role{})

		//second reconcile adds owner label to role in stage namespace
		res, err = r.Reconcile(context.TODO(), req)
		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev", "stage").
			HasConditions(Updating())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
		AssertThatNamespace(t, spacename+"-stage", fakeClient).
			HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
			HasResource("crtadmin-view", &rbacv1.RoleBinding{}).
			ResourceHasOwnerLabel("exec-pods", &rbacv1.Role{}, spacename)
	})

	t.Run("no NSTemplateSet available", func(t *testing.T) {
		// given
		r, req, _ := prepareReconcile(t, namespaceName, spacename)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
	})
}

func TestProvisionTwoUsers(t *testing.T) {

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	// given
	spacename := "john"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("provision john's ClusterResourceQuota first", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasSpecNamespaces("dev").
			HasConditions(Provisioning())
		AssertThatNamespace(t, spacename+"-dev", fakeClient).
			DoesNotExist()
		AssertThatCluster(t, fakeClient).
			HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}). // created
			HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

		t.Run("provision john's clusterRoleBinding", func(t *testing.T) {
			// when
			res, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer().
				HasSpecNamespaces("dev").
				HasConditions(Provisioning())
			AssertThatNamespace(t, spacename+"-dev", fakeClient).
				DoesNotExist()
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}) // created

			t.Run("provision john's dev and stage Idlers", func(t *testing.T) {
				// when
				res, err := r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasSpecNamespaces("dev").
					HasConditions(Provisioning())
				AssertThatNamespace(t, spacename+"-dev", fakeClient).
					DoesNotExist()
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}) // created

				// when
				res, err = r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, res)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasConditions(Provisioning())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
					HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
					HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{}) // created
				AssertThatNamespace(t, spacename+"-dev", fakeClient).DoesNotExist()

				t.Run("provision john's dev namespace", func(t *testing.T) {
					// when
					res, err := r.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					assert.Equal(t, reconcile.Result{}, res)
					AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
						HasFinalizer().
						HasSpecNamespaces("dev").
						HasConditions(Provisioning())
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
						HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
						HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}).
						HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{})
					AssertThatNamespace(t, spacename+"-dev", fakeClient).
						HasLabel("toolchain.dev.openshift.com/owner", spacename).
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasNoLabel("toolchain.dev.openshift.com/templateref"). // no label until all the namespace inner resources have been created
						HasNoLabel("toolchain.dev.openshift.com/tier").
						HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue)

					t.Run("provision john's inner resources of dev namespace", func(t *testing.T) {
						// given - when host cluster is not ready, then it should use the cache
						r.GetHostCluster = NewGetHostCluster(fakeClient, true, corev1.ConditionFalse)

						// when
						res, err := r.Reconcile(context.TODO(), req)

						// then
						require.NoError(t, err)
						assert.Equal(t, reconcile.Result{}, res)
						AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
							HasFinalizer().
							HasSpecNamespaces("dev").
							HasConditions(Provisioning())
						AssertThatNamespace(t, spacename+"-dev", fakeClient).
							HasLabel("toolchain.dev.openshift.com/owner", spacename).
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
							HasLabel("toolchain.dev.openshift.com/tier", "advanced").
							HasLabel(toolchainv1alpha1.ProviderLabelKey, toolchainv1alpha1.ProviderLabelValue).
							HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
						AssertThatCluster(t, fakeClient).
							HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
							HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

						t.Run("provision ClusterResourceQuota for the joe user (using cached TierTemplate)", func(t *testing.T) {
							// given
							joeUsername := "joe"
							joeNsTmplSet := newNSTmplSet(namespaceName, joeUsername, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
							err := fakeClient.Create(context.TODO(), joeNsTmplSet)
							require.NoError(t, err)
							joeReq := newReconcileRequest(namespaceName, joeUsername)

							// when
							res, err := r.Reconcile(context.TODO(), joeReq)

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
								res, err := r.Reconcile(context.TODO(), joeReq)

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

								t.Run("provision joe's dev and stage Idlers", func(t *testing.T) {
									// when
									res, err := r.Reconcile(context.TODO(), joeReq)

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
										HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
										HasResource(joeUsername+"-dev", &toolchainv1alpha1.Idler{}) // created

									// when
									res, err = r.Reconcile(context.TODO(), joeReq)

									// then
									require.NoError(t, err)
									assert.Equal(t, reconcile.Result{}, res)
									AssertThatNSTemplateSet(t, namespaceName, joeUsername, fakeClient).
										HasConditions(Provisioning())
									AssertThatCluster(t, fakeClient).
										HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{}).
										HasResource(joeUsername+"-dev", &toolchainv1alpha1.Idler{}).
										HasResource(joeUsername+"-stage", &toolchainv1alpha1.Idler{}) // created

									t.Run("provision joe's dev namespace (using cached TierTemplate)", func(t *testing.T) {
										// when
										res, err := r.Reconcile(context.TODO(), joeReq)

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
											HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{}).
											HasResource(joeUsername+"-tekton-view", &rbacv1.ClusterRoleBinding{})

										t.Run("provision inner resources of joe's dev namespace (using cached TierTemplate)", func(t *testing.T) {
											// given - when host cluster is not ready, then it should use the cache
											r.GetHostCluster = NewGetHostCluster(fakeClient, true, corev1.ConditionFalse)

											// when
											res, err := r.Reconcile(context.TODO(), joeReq)

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
												HasResource("crtadmin-pods", &rbacv1.RoleBinding{})
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("upgrade from basic to advanced tier", func(t *testing.T) {

		t.Run("create ClusterResourceQuota", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev"), withClusterResources("abcde11"))
			// create namespace (and assume it is complete since it has the expected revision number)
			devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
			stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
			devRo := newRole(devNS.Name, "exec-pods", spacename)
			stageRo := newRole(stageNS.Name, "exec-pods", spacename)
			devRb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
			devRb2 := newRoleBinding(devNS.Name, "crtadmin-view", spacename)
			stageRb := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
			stageRb2 := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)
			r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, devRo, stageRo, devRb, devRb2, stageRb, stageRb2)

			err := fakeClient.Update(context.TODO(), nsTmplSet)
			require.NoError(t, err)

			// when
			_, err = r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")). // upgraded
				HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})

			for _, nsType := range []string{"stage", "dev"} {
				AssertThatNamespace(t, spacename+"-"+nsType, r.Client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", spacename).
					HasLabel("toolchain.dev.openshift.com/templateref", "basic-"+nsType+"-abcde11"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/tier", "basic").
					HasLabel("toolchain.dev.openshift.com/type", nsType).
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("exec-pods", &rbacv1.Role{})
			}

			t.Run("create ClusterRoleBinding", func(t *testing.T) {
				// when
				_, err = r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
						WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
						WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
				for _, nsType := range []string{"stage", "dev"} {
					AssertThatNamespace(t, spacename+"-"+nsType, r.Client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/templateref", "basic-"+nsType+"-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/owner", spacename).
						HasLabel("toolchain.dev.openshift.com/tier", "basic"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/type", nsType).
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasResource("exec-pods", &rbacv1.Role{})
				}

				t.Run("create 2 Idlers", func(t *testing.T) {
					// when
					_, err = r.Reconcile(context.TODO(), req)
					// then
					require.NoError(t, err)
					AssertThatCluster(t, fakeClient).
						HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{},
							WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
							WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // created

					// when
					_, err = r.Reconcile(context.TODO(), req)
					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
						HasFinalizer().
						HasConditions(Updating())
					AssertThatCluster(t, fakeClient).
						HasResource(spacename+"-dev", &toolchainv1alpha1.Idler{}). // still exists (no need to check again the labels)
						HasResource(spacename+"-stage", &toolchainv1alpha1.Idler{},
							WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
							WithLabel("toolchain.dev.openshift.com/tier", "advanced")) // created

					t.Run("delete redundant namespace", func(t *testing.T) {

						// when - should delete the -stage namespace
						_, err := r.Reconcile(context.TODO(), req)

						// then
						require.NoError(t, err)
						AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
							HasFinalizer().
							HasConditions(Updating())
						AssertThatCluster(t, fakeClient).
							HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
								WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
								WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
						AssertThatNamespace(t, stageNS.Name, r.Client).
							DoesNotExist() // namespace was deleted
						AssertThatNamespace(t, devNS.Name, r.Client).
							HasNoOwnerReference().
							HasLabel("toolchain.dev.openshift.com/owner", spacename).
							HasLabel("toolchain.dev.openshift.com/templateref", "basic-dev-abcde11").
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
							HasLabel("toolchain.dev.openshift.com/tier", "basic") // not upgraded yet

						t.Run("upgrade the dev namespace", func(t *testing.T) {
							// when - should upgrade the namespace
							_, err = r.Reconcile(context.TODO(), req)

							// then
							require.NoError(t, err)
							// NSTemplateSet provisioning is complete
							AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
								HasFinalizer().
								HasConditions(Updating())
							AssertThatCluster(t, fakeClient).
								HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
									WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
									WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
							AssertThatNamespace(t, stageNS.Name, r.Client).
								DoesNotExist()
							AssertThatNamespace(t, spacename+"-dev", r.Client).
								HasNoOwnerReference().
								HasLabel("toolchain.dev.openshift.com/owner", spacename).
								HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
								HasLabel("toolchain.dev.openshift.com/tier", "advanced").
								HasLabel("toolchain.dev.openshift.com/type", "dev").
								HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
								HasResource("exec-pods", &rbacv1.Role{}).
								HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
								HasResource("crtadmin-view", &rbacv1.RoleBinding{})

							t.Run("when nothing to upgrade, then it should be provisioned", func(t *testing.T) {
								// given - when host cluster is not ready, then it should use the cache (for both TierTemplates)
								r.GetHostCluster = NewGetHostCluster(fakeClient, true, corev1.ConditionFalse)

								// when - should check if everything is OK and set status to provisioned
								_, err = r.Reconcile(context.TODO(), req)

								// then
								require.NoError(t, err)
								// NSTemplateSet provisioning is complete
								AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
									HasFinalizer().
									HasConditions(Provisioned())
								AssertThatCluster(t, fakeClient).
									HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
										WithLabel("toolchain.dev.openshift.com/tier", "advanced"))
								AssertThatNamespace(t, spacename+"-dev", r.Client).
									HasNoOwnerReference().
									HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11").
									HasLabel("toolchain.dev.openshift.com/owner", spacename).
									HasLabel("toolchain.dev.openshift.com/tier", "advanced"). // not updgraded yet
									HasLabel("toolchain.dev.openshift.com/type", "dev").
									HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
									HasResource("crtadmin-pods", &rbacv1.RoleBinding{}) // role has been removed
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
	t.Cleanup(restore)

	t.Run("upgrade from abcde11 to abcde12 as part of the advanced tier", func(t *testing.T) {

		t.Run("update ClusterResourceQuota", func(t *testing.T) {
			// given
			nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde12", "dev"), withClusterResources("abcde12"))

			devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
			devRo := newRole(devNS.Name, "exec-pods", spacename)
			devRb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
			devRbacRb := newRoleBinding(devNS.Name, "crtadmin-view", spacename)

			stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
			stageRo := newRole(stageNS.Name, "exec-pods", spacename)
			stageRb := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
			stageRbacRb := newRoleBinding(stageNS.Name, "crtadmin-view", spacename)

			crb := newTektonClusterRoleBinding(spacename, "advanced")
			crq := newClusterResourceQuota(spacename, "advanced")
			r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet,
				devNS, devRo, devRb, devRbacRb, stageNS, stageRo, stageRb, stageRbacRb, crq, crb)

			err := fakeClient.Update(context.TODO(), nsTmplSet)
			require.NoError(t, err)

			// when
			_, err = r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
				HasFinalizer().
				HasConditions(Updating())
			AssertThatCluster(t, fakeClient).
				HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced")). // upgraded
				HasResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{},
					WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde11"),
					WithLabel("toolchain.dev.openshift.com/tier", "advanced"))

			for _, nsType := range []string{"stage", "dev"} {
				AssertThatNamespace(t, spacename+"-"+nsType, r.Client).
					HasNoOwnerReference().
					HasLabel("toolchain.dev.openshift.com/owner", spacename).
					HasLabel("toolchain.dev.openshift.com/templateref", "advanced-"+nsType+"-abcde11"). // not upgraded yet
					HasLabel("toolchain.dev.openshift.com/tier", "advanced").
					HasLabel("toolchain.dev.openshift.com/type", nsType).
					HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
					HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
					HasResource("exec-pods", &rbacv1.Role{}).
					HasResource("crtadmin-view", &rbacv1.RoleBinding{})
			}

			t.Run("delete ClusterRoleBinding", func(t *testing.T) {
				// when
				_, err = r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
					HasFinalizer().
					HasConditions(Updating())
				AssertThatCluster(t, fakeClient).
					HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
														WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
														WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
					HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{}) // deleted
				for _, nsType := range []string{"stage", "dev"} {
					AssertThatNamespace(t, spacename+"-"+nsType, r.Client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", spacename).
						HasLabel("toolchain.dev.openshift.com/templateref", "advanced-"+nsType+"-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/tier", "advanced").
						HasLabel("toolchain.dev.openshift.com/type", nsType).
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
						HasResource("exec-pods", &rbacv1.Role{}).
						HasResource("crtadmin-view", &rbacv1.RoleBinding{})
				}

				t.Run("delete redundant namespace", func(t *testing.T) {

					// when - should delete the -stage namespace
					_, err := r.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
						HasFinalizer().
						HasConditions(Updating())
					AssertThatCluster(t, fakeClient).
						HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
							WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
							WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
						HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
					AssertThatNamespace(t, stageNS.Name, r.Client).
						DoesNotExist() // namespace was deleted
					AssertThatNamespace(t, devNS.Name, r.Client).
						HasNoOwnerReference().
						HasLabel("toolchain.dev.openshift.com/owner", spacename).
						HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde11"). // not upgraded yet
						HasLabel("toolchain.dev.openshift.com/type", "dev").
						HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
						HasLabel("toolchain.dev.openshift.com/tier", "advanced").
						HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
						HasResource("exec-pods", &rbacv1.Role{}).
						HasResource("crtadmin-view", &rbacv1.RoleBinding{})

					t.Run("upgrade the dev namespace", func(t *testing.T) {
						// when - should upgrade the namespace
						_, err = r.Reconcile(context.TODO(), req)

						// then
						require.NoError(t, err)
						// NSTemplateSet provisioning is complete
						AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
							HasFinalizer().
							HasConditions(Updating())
						AssertThatCluster(t, fakeClient).
							HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
								WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
								WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
							HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
						AssertThatNamespace(t, stageNS.Name, r.Client).
							DoesNotExist()
						AssertThatNamespace(t, devNS.Name, r.Client).
							HasNoOwnerReference().
							HasLabel("toolchain.dev.openshift.com/owner", spacename).
							HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde12"). // upgraded
							HasLabel("toolchain.dev.openshift.com/type", "dev").
							HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
							HasLabel("toolchain.dev.openshift.com/tier", "advanced").
							HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
							HasResource("exec-pods", &rbacv1.Role{}).
							HasNoResource("crtadmin-view", &rbacv1.RoleBinding{})

						t.Run("when nothing to update, then it should be provisioned", func(t *testing.T) {
							// given - when host cluster is not ready, then it should use the cache (for both TierTemplates)
							r.GetHostCluster = NewGetHostCluster(fakeClient, true, corev1.ConditionFalse)

							// when - should check if everything is OK and set status to provisioned
							_, err = r.Reconcile(context.TODO(), req)

							// then
							require.NoError(t, err)
							// NSTemplateSet provisioning is complete
							AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
								HasFinalizer().
								HasConditions(Provisioned())
							AssertThatCluster(t, fakeClient).
								HasResource("for-"+spacename, &quotav1.ClusterResourceQuota{},
									WithLabel("toolchain.dev.openshift.com/templateref", "advanced-clusterresources-abcde12"),
									WithLabel("toolchain.dev.openshift.com/tier", "advanced")).
								HasNoResource(spacename+"-tekton-view", &rbacv1.ClusterRoleBinding{})
							AssertThatNamespace(t, stageNS.Name, r.Client).
								DoesNotExist()
							AssertThatNamespace(t, devNS.Name, r.Client).
								HasNoOwnerReference().
								HasLabel("toolchain.dev.openshift.com/owner", spacename).
								HasLabel("toolchain.dev.openshift.com/templateref", "advanced-dev-abcde12"). // upgraded
								HasLabel("toolchain.dev.openshift.com/type", "dev").
								HasLabel("toolchain.dev.openshift.com/provider", "codeready-toolchain").
								HasLabel("toolchain.dev.openshift.com/tier", "advanced").
								HasResource("crtadmin-pods", &rbacv1.RoleBinding{}).
								HasResource("exec-pods", &rbacv1.Role{}).
								HasNoResource("crtadmin-view", &rbacv1.RoleBinding{})
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
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("fail to get nstmplset", func(t *testing.T) {
		// given
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename)
		fakeClient.MockGet = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return errors.New("unable to get NSTemplate")
		}

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to get NSTemplate")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("fail to update status", func(t *testing.T) {
		// given
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			return errors.New("unable to update status")
		}

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update status")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasNoConditions() // since we're unable to update the status
	})

	t.Run("no namespace", func(t *testing.T) {
		// given
		r, _ := prepareController(t)
		req := newReconcileRequest("", spacename)

		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WATCH_NAMESPACE must be set")
		assert.Equal(t, reconcile.Result{}, res)
	})

	t.Run("fail to set provisioned namespaces list", func(t *testing.T) {
		// given
		restore := test.SetEnvVarAndRestore(t, commonconfig.WatchNamespaceEnvVar, "my-member-operator-namespace")
		t.Cleanup(restore)
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withNamespaces("abcde11", "dev", "stage"))
		devNS := newNamespace("basic", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("basic", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		rb := newRoleBinding(devNS.Name, "crtadmin-pods", spacename)
		rb2 := newRoleBinding(stageNS.Name, "crtadmin-pods", spacename)
		r, req, fakeClient := prepareReconcile(t, namespaceName, spacename, nsTmplSet, devNS, stageNS, rb, rb2)
		fakeClient.MockStatusUpdate = func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			if nsTmpl, ok := obj.(*toolchainv1alpha1.NSTemplateSet); ok {
				if len(nsTmpl.Status.ProvisionedNamespaces) > 0 {
					return errors.New("unable to update provisioned namespaces list")
				}
			}
			return nil
		}
		// when
		res, err := r.Reconcile(context.TODO(), req)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to update provisioned namespaces list")
		assert.Equal(t, reconcile.Result{}, res)
		AssertThatNSTemplateSet(t, namespaceName, spacename, fakeClient).
			HasFinalizer().
			HasNoConditions().
			HasNoProvisionedNamespaces() // since we're unable to update the provisioned namespaces in status
	})
}

func TestDeleteNSTemplateSet(t *testing.T) {
	spacename := "johnsmith"
	namespaceName := "toolchain-member"

	t.Run("with cluster resources and 2 user namespaces to delete", func(t *testing.T) {
		// given an NSTemplateSet resource and 2 active user namespaces ("dev" and "stage")
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"), withDeletionTs(), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(spacename, "advanced")
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))
		r, _ := prepareController(t, nsTmplSet, crq, devNS, stageNS)
		req := newReconcileRequest(namespaceName, spacename)

		t.Run("reconcile after nstemplateset deletion triggers deletion of the first namespace", func(t *testing.T) {
			// when a first reconcile loop was triggered (because a cluster resource quota was deleted)
			_, err := r.Reconcile(context.TODO(), req)

			// then
			require.NoError(t, err)
			// get the first namespace and check its deletion timestamp
			firstNSName := fmt.Sprintf("%s-dev", spacename)
			AssertThatNamespace(t, firstNSName, r.Client).DoesNotExist()
			// get the NSTemplateSet resource again and check its status
			AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
				HasFinalizer(). // the finalizer should NOT have been removed yet
				HasConditions(Terminating())

			t.Run("reconcile after first user namespace deletion triggers deletion of the second namespace", func(t *testing.T) {
				// when a second reconcile loop was triggered (because a user namespace was deleted)
				_, err := r.Reconcile(context.TODO(), req)

				// then
				require.NoError(t, err)
				// get the second namespace and check its deletion timestamp
				secondtNSName := fmt.Sprintf("%s-dev", spacename)
				AssertThatNamespace(t, secondtNSName, r.Client).DoesNotExist()
				// get the NSTemplateSet resource again and check its finalizers and status
				AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
					HasFinalizer(). // the finalizer should not have been removed either
					HasConditions(Terminating())

				t.Run("reconcile after second user namespace deletion triggers deletion of CRQ", func(t *testing.T) {
					// when
					_, err := r.Reconcile(context.TODO(), req)

					// then
					require.NoError(t, err)
					AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
						HasFinalizer(). // the finalizer should NOT have been removed yet
						HasConditions(Terminating())
					AssertThatCluster(t, r.Client).
						HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{}) // resource was deleted

					t.Run("reconcile after cluster resource quota deletion triggers removal of the finalizer and thus successful deletion", func(t *testing.T) {
						// given - when host cluster is not ready, then it should use the cache
						r.GetHostCluster = NewGetHostCluster(r.Client, true, corev1.ConditionFalse)

						// when a last reconcile loop is triggered (when the NSTemplateSet resource is marked for deletion and there's a finalizer)
						_, err := r.Reconcile(context.TODO(), req)

						// then
						require.NoError(t, err)
						// get the NSTemplateSet resource again and check its finalizers and status
						AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
							DoesNotExist()
						AssertThatCluster(t, r.Client).HasNoResource("for-"+spacename, &quotav1.ClusterResourceQuota{})
					})
				})
			})
		})
	})

	t.Run("failed to delete cluster resources", func(t *testing.T) {
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withDeletionTs(), withClusterResources("abcde11"))
		crq := newClusterResourceQuota(spacename, "advanced")
		r, fakeClient := prepareController(t, nsTmplSet, crq)
		req := newReconcileRequest(namespaceName, spacename)

		// only add deletion timestamp, but not delete
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			t.Logf("deleting resource of kind %T '%s'", obj, obj.GetName())
			if _, ok := obj.(*quotav1.ClusterResourceQuota); ok {
				return fmt.Errorf("mock error")
			}
			return fakeClient.Client.Delete(ctx, obj, opts...)
		}

		// first reconcile, deletion is triggered and fails
		result, err := r.Reconcile(context.TODO(), req)
		// then
		require.EqualError(t, err, "failed to delete cluster resource 'for-johnsmith': mock error")
		require.Empty(t, result)
	})

	t.Run("delete when there is no finalizer", func(t *testing.T) {
		// given an NSTemplateSet resource which is being deleted and whose finalizer was already removed
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "basic", withoutFinalizer(), withDeletionTs(), withClusterResources("abcde11"), withNamespaces("abcde11", "dev", "stage"))
		r, req, _ := prepareReconcile(t, namespaceName, spacename, nsTmplSet)

		// when a reconcile loop is triggered
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.NoError(t, err)
		AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
			DoesNotHaveFinalizer() // finalizer was not added and nothing else was done
	})

	t.Run("NSTemplateSet not deleted until namespace is deleted", func(t *testing.T) {
		// given an NSTemplateSet resource and 1 active user namespaces ("dev")
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"), withDeletionTs(), withClusterResources("abcde11"))
		nsTmplSet.SetDeletionTimestamp(&metav1.Time{Time: time.Now().Add(-61 * time.Second)})
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))

		r, fakeClient := prepareController(t, nsTmplSet, devNS, stageNS)
		req := newReconcileRequest(namespaceName, spacename)

		// only add deletion timestamp, but not delete
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			if obj, ok := obj.(*corev1.Namespace); ok {
				deletionTs := metav1.Now()
				obj.DeletionTimestamp = &deletionTs
				if err := r.Client.Update(context.TODO(), obj); err != nil {
					return err
				}
			}
			return nil
		}
		// when
		_, err := r.Reconcile(context.TODO(), req)

		// then
		require.EqualError(t, err, "NSTemplateSet deletion has not completed in over 1 minute")
	})

	t.Run("NSTemplateSet not deleted until namespace is deleted", func(t *testing.T) {
		// given an NSTemplateSet resource and 1 active user namespaces ("dev")
		nsTmplSet := newNSTmplSet(namespaceName, spacename, "advanced", withNamespaces("abcde11", "dev", "stage"), withDeletionTs(), withClusterResources("abcde11"))
		devNS := newNamespace("advanced", spacename, "dev", withTemplateRefUsingRevision("abcde11"))
		stageNS := newNamespace("advanced", spacename, "stage", withTemplateRefUsingRevision("abcde11"))

		r, fakeClient := prepareController(t, nsTmplSet, devNS, stageNS)
		req := newReconcileRequest(namespaceName, spacename)

		// only add deletion timestamp, but not delete
		fakeClient.MockDelete = func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			if obj, ok := obj.(*corev1.Namespace); ok {
				if len(obj.Finalizers) == 0 {
					deletionTs := metav1.Now()
					obj.DeletionTimestamp = &deletionTs
					// we need to set finalizer, otherwise, the fakeclient would delete it as soon as the deletion timestamp is set
					obj.Finalizers = []string{"kubernetes"}
				} else {
					obj.Finalizers = nil
				}
				if err := r.Client.Update(context.TODO(), obj); err != nil {
					return err
				}
			}
			return nil
		}
		// first reconcile, deletion is triggered
		result, err := r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		require.True(t, result.Requeue)
		require.Equal(t, time.Second, result.RequeueAfter)
		//then second reconcile to check if namespace has actually been deleted
		result, err = r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		require.True(t, result.Requeue)
		require.Equal(t, time.Second, result.RequeueAfter)

		firstNSName := fmt.Sprintf("%s-dev", spacename)
		secondNSName := fmt.Sprintf("%s-stage", spacename)
		// get the first namespace and check that it has deletion timestamp
		AssertThatNamespace(t, firstNSName, r.Client).HasDeletionTimestamp()
		//second NS is not affected
		AssertThatNamespace(t, secondNSName, r.Client).HasNoDeletionTimestamp()
		// get the NSTemplateSet resource again, check it is not deleted and its status
		AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
			HasFinalizer().
			HasConditions(Terminating())

		//reconcile to check there is no change, ns still exists
		result, err = r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		require.True(t, result.Requeue)
		require.Equal(t, time.Second, result.RequeueAfter)

		AssertThatNamespace(t, firstNSName, r.Client).HasDeletionTimestamp()
		AssertThatNamespace(t, secondNSName, r.Client).HasNoDeletionTimestamp()
		// get the NSTemplateSet resource again, check it is not deleted and its status
		AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
			HasFinalizer().
			HasConditions(Terminating())

		// actually delete ns
		ns := &corev1.Namespace{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: firstNSName}, ns)
		require.NoError(t, err)
		err = fakeClient.Delete(context.TODO(), ns)
		require.NoError(t, err)

		// set MockDelete to nil
		fakeClient.MockDelete = nil //now removing the mockDelete

		// deletion of firstNS would trigger another reconcile deleting secondNS
		result, err = r.Reconcile(context.TODO(), req)
		require.NoError(t, err)
		require.True(t, result.Requeue)
		require.Equal(t, time.Second, result.RequeueAfter)

		// get the first namespace and check it IS deleted
		AssertThatNamespace(t, firstNSName, r.Client).DoesNotExist()

		// Check that nsTemplateSet still has finalizer
		AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
			HasFinalizer().HasConditions(Terminating())

		// deletion of secondNS would trigger another reconcile
		result, err = r.Reconcile(context.TODO(), req)
		require.Empty(t, result)
		require.NoError(t, err)

		AssertThatNamespace(t, secondNSName, r.Client).DoesNotExist()
		// Check that nsTemplateSet is gone as well
		AssertThatNSTemplateSet(t, namespaceName, spacename, r.Client).
			DoesNotExist()

	})
}

func prepareReconcile(t *testing.T, namespaceName, name string, initObjs ...runtime.Object) (*Reconciler, reconcile.Request, *test.FakeClient) {
	r, fakeClient := prepareController(t, initObjs...)
	return r, newReconcileRequest(namespaceName, name), fakeClient
}

func prepareStatusManager(t *testing.T, initObjs ...runtime.Object) (*statusManager, *test.FakeClient) {
	apiClient, fakeClient := prepareAPIClient(t, initObjs...)
	return &statusManager{
		APIClient: apiClient,
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
	return NewReconciler(apiClient), fakeClient
}

func toStructured(obj client.Object, decoder runtime.Decoder) (client.Object, error) {
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
			ns := &corev1.Namespace{}
			_, _, err = decoder.Decode(data, nil, ns)
			ns.Status = corev1.NamespaceStatus{Phase: corev1.NamespaceActive}
			return ns, err
		case "Idler":
			idler := &toolchainv1alpha1.Idler{}
			_, _, err = decoder.Decode(data, nil, idler)
			return idler, err
		case "Role":
			rl := &rbacv1.Role{}
			_, _, err = decoder.Decode(data, nil, rl)
			return rl, err
		case "RoleBinding":
			rolebinding := &rbacv1.RoleBinding{}
			_, _, err = decoder.Decode(data, nil, rolebinding)
			return rolebinding, err
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

func newNSTmplSet(namespaceName, name, tier string, options ...nsTmplSetOption) *toolchainv1alpha1.NSTemplateSet { // nolint:unparam
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

func withSpaceRoles(roles map[string][]string) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Spec.SpaceRoles = make([]toolchainv1alpha1.NSTemplateSetSpaceRole, 0, len(roles))
		for ref, usernames := range roles {
			nsTmplSet.Spec.SpaceRoles = append(nsTmplSet.Spec.SpaceRoles, toolchainv1alpha1.NSTemplateSetSpaceRole{
				TemplateRef: ref,
				Usernames:   usernames,
			})
		}
	}
}

func withConditions(conditions ...toolchainv1alpha1.Condition) nsTmplSetOption {
	return func(nsTmplSet *toolchainv1alpha1.NSTemplateSet) {
		nsTmplSet.Status.Conditions = conditions
	}
}

func newNamespace(tier, owner, typeName string, options ...objectMetaOption) *corev1.Namespace {
	labels := map[string]string{
		"toolchain.dev.openshift.com/owner":    owner,
		"toolchain.dev.openshift.com/type":     typeName,
		"toolchain.dev.openshift.com/provider": "codeready-toolchain",
	}
	if tier != "" {
		labels["toolchain.dev.openshift.com/templateref"] = NewTierTemplateName(tier, typeName, "abcde11")
		labels["toolchain.dev.openshift.com/tier"] = tier
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("%s-%s", owner, typeName),
			Labels: labels,
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	for _, set := range options {
		ns.ObjectMeta = set(ns.ObjectMeta, tier, typeName)
	}
	return ns
}

func newRoleBinding(namespace, name, owner string) *rbacv1.RoleBinding { //nolint: unparam
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				"toolchain.dev.openshift.com/owner":    owner,
			},
		},
	}
}

func newRole(namespace, name, owner string) *rbacv1.Role { //nolint: unparam
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider": "codeready-toolchain",
				"toolchain.dev.openshift.com/owner":    owner,
			},
		},
	}
}

func newTektonClusterRoleBinding(spacename, tier string) *rbacv1.ClusterRoleBinding {
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
				"toolchain.dev.openshift.com/owner":       spacename,
				"toolchain.dev.openshift.com/type":        "clusterresources",
			},
			Name:       spacename + "-tekton-view",
			Generation: int64(1),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "tekton-view-for-" + spacename,
		},
		Subjects: []rbacv1.Subject{{
			Kind: "User",
			Name: spacename,
		}},
	}
	return crb
}

func newClusterResourceQuota(spacename, tier string, options ...objectMetaOption) *quotav1.ClusterResourceQuota {
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
				"toolchain.dev.openshift.com/owner":       spacename,
				"toolchain.dev.openshift.com/type":        "clusterresources",
			},
			Annotations: map[string]string{},
			Name:        "for-" + spacename,
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
					"openshift.io/requester": spacename,
				},
			},
		},
	}
	for _, option := range options {
		crq.ObjectMeta = option(crq.ObjectMeta, tier, "clusterresources")
	}
	return crq
}

func newIdler(spacename, name, tierName string) *toolchainv1alpha1.Idler { // nolint
	idler := &toolchainv1alpha1.Idler{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Idler",
			APIVersion: toolchainv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"toolchain.dev.openshift.com/provider":    "codeready-toolchain",
				"toolchain.dev.openshift.com/tier":        tierName,
				"toolchain.dev.openshift.com/templateref": NewTierTemplateName(tierName, "clusterresources", "abcde11"),
				"toolchain.dev.openshift.com/owner":       spacename,
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

func withLabels(labels map[string]string) objectMetaOption {
	return func(meta metav1.ObjectMeta, tier, typeName string) metav1.ObjectMeta {
		for k, v := range labels {
			meta.Labels[k] = v
		}
		return meta
	}
}

func withTemplateRefUsingRevision(revision string) objectMetaOption {
	return func(meta metav1.ObjectMeta, tier, typeName string) metav1.ObjectMeta {
		meta.Labels["toolchain.dev.openshift.com/templateref"] = NewTierTemplateName(tier, typeName, revision)
		return meta
	}
}

func withLastAppliedSpaceRoles(nsTmplSet *toolchainv1alpha1.NSTemplateSet) objectMetaOption {
	return func(meta metav1.ObjectMeta, tier, typeName string) metav1.ObjectMeta {
		sr, _ := json.Marshal(nsTmplSet.Spec.SpaceRoles) // assume marshalling always works
		if meta.Annotations == nil {
			meta.Annotations = map[string]string{}
		}
		meta.Annotations[toolchainv1alpha1.LastAppliedSpaceRolesAnnotationKey] = string(sr)
		return meta
	}
}

func prepareTemplateTiers(decoder runtime.Decoder) ([]runtime.Object, error) {
	var tierTemplates []runtime.Object

	// templates indexed by tiername / type / revision
	tmpls := map[string]map[string]map[string]string{
		"advanced": {
			"clusterresources": {
				"abcde11": test.CreateTemplate(test.WithObjects(advancedCrq, clusterTektonRb, idlerDev, idlerStage), test.WithParams(spacename, username)),
				"abcde12": test.CreateTemplate(test.WithObjects(advancedCrq), test.WithParams(spacename)),
			},
			"dev": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
			},
			"stage": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
			},
			"admin": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceAdmin, spaceAdminRb), test.WithParams(namespace, username)),
			},
		},
		"basic": {
			// no clusterresources
			"dev": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb), test.WithParams(spacename)),
				"abcde13": test.CreateTemplate(test.WithObjects(nsWithArgoLabel, crtAdminRb), test.WithParams(spacename)), // ns label change
			},
			"stage": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb), test.WithParams(spacename)),
			},
			"admin": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceAdmin, spaceAdminRb), test.WithParams(namespace, username)),
			},
		},
		"team": {
			"clusterresources": {
				"abcde11": test.CreateTemplate(test.WithObjects(teamCrq, clusterTektonRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(teamCrq, clusterTektonRb), test.WithParams(spacename)),
			},
			"dev": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
			},
			"stage": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
			},
			"admin": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceAdmin, spaceAdminRb), test.WithParams(namespace, username)),
			},
		},
		"withemptycrq": {
			"clusterresources": {
				"abcde11": test.CreateTemplate(test.WithObjects(advancedCrq, emptyCrq, clusterTektonRb), test.WithParams(spacename, username)),
				"abcde12": test.CreateTemplate(test.WithObjects(advancedCrq, emptyCrq, clusterTektonRb), test.WithParams(spacename, username)),
			},
			"dev": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
			},
			"stage": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
				"abcde12": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole), test.WithParams(spacename)),
			},
			"admin": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceAdmin, spaceAdminRb), test.WithParams(namespace, username)),
			},
		},
		"appstudio": {
			"clusterresources": {
				"abcde11": test.CreateTemplate(test.WithObjects(advancedCrq, clusterTektonRb, idlerDev, idlerStage), test.WithParams(spacename, username)),
			},
			"appstudio": {
				"abcde11": test.CreateTemplate(test.WithObjects(ns, crtAdminRb, execPodsRole, crtAdminViewRb), test.WithParams(spacename)),
			},
			"admin": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceAdmin, spaceAdminRb), test.WithParams(namespace, username)),
			},
			"viewer": { // space roles
				"abcde11": test.CreateTemplate(test.WithObjects(spaceViewer, spaceViewerRb), test.WithParams(namespace, username)),
			},
		},
	}
	for tierName, tierTmpls := range tmpls {
		for typeName, typeTmpls := range tierTmpls {
			for revision, tmpl := range typeTmpls {
				tierTmpl, err := createTierTemplate(decoder, tmpl, tierName, typeName, revision)
				if err != nil {
					return nil, err
				}
				tierTemplates = append(tierTemplates, tierTmpl)
			}
		}
	}
	return tierTemplates, nil
}

func createTierTemplate(decoder runtime.Decoder, tmplContent string, tierName, typeName, revision string) (*toolchainv1alpha1.TierTemplate, error) {
	tmplContent = strings.ReplaceAll(tmplContent, "NSTYPE", typeName)
	tmpl := templatev1.Template{}
	_, _, err := decoder.Decode([]byte(tmplContent), nil, &tmpl)
	if err != nil {
		return nil, err
	}
	return &toolchainv1alpha1.TierTemplate{
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
	}, nil
}

var (
	ns test.TemplateObject = `
- apiVersion: v1
  kind: Namespace
  metadata:
    name: ${SPACE_NAME}-NSTYPE
`

	nsWithArgoLabel test.TemplateObject = `
- apiVersion: v1
  kind: Namespace
  metadata:
    name: ${SPACE_NAME}-NSTYPE
    labels:
      argocd.argoproj.io/managed-by: gitops-service-argocd
`

	execPodsRole test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: exec-pods
    namespace: ${SPACE_NAME}-NSTYPE
  rules:
  - apiGroups:
    - ""
    resources:
    - pods/exec
    verbs:
    - get
    - list
    - watch
    - create
    - delete
    - update`

	crtAdminRb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: RoleBinding
  metadata:
    name: crtadmin-pods
    namespace: ${SPACE_NAME}-NSTYPE
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: Role
    name: exec-pods
  subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: crtadmin-users-view`

	crtAdminViewRb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: RoleBinding
  metadata:
    name: crtadmin-view
    namespace: ${SPACE_NAME}-dev
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: view
  subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: crtadmin-users-view
`

	namespace test.TemplateParam = `
- name: NAMESPACE
  required: true`
	spacename test.TemplateParam = `
- name: SPACE_NAME
  value: johnsmith`
	username test.TemplateParam = `
- name: USERNAME
  value: johnsmith`

	advancedCrq test.TemplateObject = `
- apiVersion: quota.openshift.io/v1
  kind: ClusterResourceQuota
  metadata:
    name: for-${SPACE_NAME}
  spec:
    quota:
      hard:
        limits.cpu: 2000m
        limits.memory: 10Gi
    selector:
      annotations:
        openshift.io/requester: ${SPACE_NAME}
    labels: null
  `
	teamCrq test.TemplateObject = `
- apiVersion: quota.openshift.io/v1
  kind: ClusterResourceQuota
  metadata:
    name: for-${SPACE_NAME}
  spec:
    quota:
      hard:
        limits.cpu: 4000m
        limits.memory: 15Gi
    selector:
      annotations:
        openshift.io/requester: ${SPACE_NAME}
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
    name: ${SPACE_NAME}-tekton-view
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: tekton-view-for-${SPACE_NAME}
  subjects:
    - kind: User
      name: ${USERNAME}
`
	idlerDev test.TemplateObject = `
- apiVersion: toolchain.dev.openshift.com/v1alpha1
  kind: Idler
  metadata:
    name: ${SPACE_NAME}-dev
  spec:
    timeoutSeconds: 28800 # 8 hours
  `
	idlerStage test.TemplateObject = `
- apiVersion: toolchain.dev.openshift.com/v1alpha1
  kind: Idler
  metadata:
    name: ${SPACE_NAME}-stage
  spec:
    timeoutSeconds: 28800 # 8 hours
  `

	spaceAdmin test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: space-admin
    namespace: ${NAMESPACE}
  rules:
    # examples
    - apiGroups:
        - ""
      resources:
        - "configmaps"
        - "secrets"
        - "serviceaccounts"
      verbs:
        - get
        - list
  `
	spaceAdminRb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: RoleBinding
  metadata:
    name: ${USERNAME}-space-admin
    namespace: ${NAMESPACE}
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: Role
    name: space-admin
  subjects:
    - kind: User
      name: ${USERNAME}
`
	spaceViewer test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: space-viewer
    namespace: ${NAMESPACE}
  rules:
    # examples
    - apiGroups:
        - ""
      resources:
        - "configmaps"
      verbs:
        - get
        - list
  `
	spaceViewerRb test.TemplateObject = `
- apiVersion: rbac.authorization.k8s.io/v1
  kind: RoleBinding
  metadata:
    name: ${USERNAME}-space-viewer
    namespace: ${NAMESPACE}
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: Role
    name: space-viewer
  subjects:
    - kind: User
      name: ${USERNAME}
`
)
