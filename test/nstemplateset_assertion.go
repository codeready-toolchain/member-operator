package test

import (
	"context"
	"fmt"
	"strings"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NSTemplateSetAssertion struct {
	nsTmplSet      *toolchainv1alpha1.NSTemplateSet
	client         client.Client
	namespacedName types.NamespacedName
	t              test.T
}

func (a *NSTemplateSetAssertion) loadNSTemplateSet() error {
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := a.client.Get(context.TODO(), a.namespacedName, nsTmplSet)
	a.nsTmplSet = nsTmplSet
	return err
}

func AssertThatNSTemplateSet(t test.T, namespace, name string, client client.Client) *NSTemplateSetAssertion {
	return &NSTemplateSetAssertion{
		client:         client,
		namespacedName: test.NamespacedName(namespace, name),
		t:              t,
	}
}

func (a *NSTemplateSetAssertion) DoesNotExist() *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.Error(a.t, err)
	assert.IsType(a.t, v1.StatusReasonNotFound, errors.ReasonForError(err))
	return a
}

func (a *NSTemplateSetAssertion) HasNoConditions() *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	require.Empty(a.t, a.nsTmplSet.Status.Conditions)
	return a
}

func (a *NSTemplateSetAssertion) HasConditions(expected ...toolchainv1alpha1.Condition) *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	test.AssertConditionsMatch(a.t, a.nsTmplSet.Status.Conditions, expected...)
	return a
}

func (a *NSTemplateSetAssertion) HasTierName(tierName string) *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	assert.Equal(a.t, a.nsTmplSet.Spec.TierName, tierName)
	return a
}

func (a *NSTemplateSetAssertion) HasClusterResourcesTemplateRef(templateRef string) *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	assert.NotNil(a.t, a.nsTmplSet.Spec.ClusterResources.TemplateRef)
	assert.Equal(a.t, a.nsTmplSet.Spec.ClusterResources.TemplateRef, templateRef)
	return a
}

func (a *NSTemplateSetAssertion) HasClusterResourcesNil() *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	assert.Nil(a.t, a.nsTmplSet.Spec.ClusterResources)
	return a
}

func (a *NSTemplateSetAssertion) HasNamespaceTemplateRefs(templateRefs ...string) *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	require.Len(a.t, a.nsTmplSet.Spec.Namespaces, len(templateRefs))
TemplateRefs:
	for _, templateRef := range templateRefs {
		for _, nsRef := range a.nsTmplSet.Spec.Namespaces {
			if nsRef.TemplateRef == templateRef {
				continue TemplateRefs
			}
		}
		assert.Failf(a.t, "TemplateRef not found",
			"the TemplateRef %s wasn't found in the set of Namespace TemplateRefs %s", templateRef, a.nsTmplSet.Spec.Namespaces)
	}
	return a
}

func (a *NSTemplateSetAssertion) HasSpecNamespaces(types ...string) *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	require.Len(a.t, a.nsTmplSet.Spec.Namespaces, len(types))
	for i, nstype := range types {
		assert.Equal(a.t, NewTierTemplateName(a.nsTmplSet.Spec.TierName, nstype, "abcde11"), a.nsTmplSet.Spec.Namespaces[i].TemplateRef)
	}
	return a
}

// NewTierTemplateName: a utility func to generate a TierTemplate name, based on the given tier, type and revision.
// note: the resource name must consist of lower case alphanumeric characters, '-' or '.', and must start and end with an alphanumeric character
func NewTierTemplateName(tier, typeName, revision string) string {
	return strings.ToLower(fmt.Sprintf("%s-%s-%s", tier, typeName, revision))
}

func Provisioned() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.NSTemplateSetProvisionedReason,
	}
}

func Provisioning() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: toolchainv1alpha1.NSTemplateSetProvisioningReason,
	}
}

func Updating() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: toolchainv1alpha1.NSTemplateSetUpdatingReason,
	}
}

func UpdateFailed(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUpdateFailedReason,
		Message: msg,
	}
}

func UnableToProvision(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionReason,
		Message: msg,
	}
}

func UnableToProvisionClusterResources(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionClusterResourcesReason,
		Message: msg,
	}
}

func UnableToProvisionNamespace(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetUnableToProvisionNamespaceReason,
		Message: msg,
	}
}

func UnableToTerminate(msg string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.NSTemplateSetTerminatingFailedReason,
		Message: msg,
	}
}

func Terminating() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionFalse,
		Reason: toolchainv1alpha1.NSTemplateSetTerminatingReason,
	}
}

func (a *NSTemplateSetAssertion) HasFinalizer() *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	assert.Len(a.t, a.nsTmplSet.Finalizers, 1)
	assert.Contains(a.t, a.nsTmplSet.Finalizers, "finalizer.toolchain.dev.openshift.com")
	return a
}

func (a *NSTemplateSetAssertion) DoesNotHaveFinalizer() *NSTemplateSetAssertion {
	err := a.loadNSTemplateSet()
	require.NoError(a.t, err)
	assert.Len(a.t, a.nsTmplSet.Finalizers, 0)
	return a
}
