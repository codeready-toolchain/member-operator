package test

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MemberStatusAssertion struct {
	memberStatus   *toolchainv1alpha1.MemberStatus
	client         client.Client
	namespacedName types.NamespacedName
	t              test.T
}

func (a *MemberStatusAssertion) loadMemberStatus() error {
	memberStatus := &toolchainv1alpha1.MemberStatus{}
	err := a.client.Get(context.TODO(), a.namespacedName, memberStatus)
	a.memberStatus = memberStatus
	return err
}

func AssertThatMemberStatus(t test.T, namespace, name string, client client.Client) *MemberStatusAssertion {
	return &MemberStatusAssertion{
		client:         client,
		namespacedName: test.NamespacedName(namespace, name),
		t:              t,
	}
}

func (a *MemberStatusAssertion) Exists() *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	return a
}

func (a *MemberStatusAssertion) HasNoConditions() *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.Empty(a.t, a.memberStatus.Status.Conditions)
	return a
}

func (a *MemberStatusAssertion) HasCondition(expected toolchainv1alpha1.Condition) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	test.AssertConditionsMatch(a.t, a.memberStatus.Status.Conditions, expected)
	return a
}

func (a *MemberStatusAssertion) HasMemberOperatorConditionErrorMsg(expected string) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.Len(a.t, a.memberStatus.Status.MemberOperator.Conditions, 1)
	require.Equal(a.t, expected, a.memberStatus.Status.MemberOperator.Conditions[0].Message)
	return a
}

func (a *MemberStatusAssertion) HasMemberOperatorConditions(expected ...toolchainv1alpha1.Condition) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	test.AssertConditionsMatch(a.t, a.memberStatus.Status.MemberOperator.Conditions, expected...)
	return a
}

func (a *MemberStatusAssertion) HasMemberOperatorRevisionCheckConditions(expected ...toolchainv1alpha1.Condition) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	test.AssertConditionsMatch(a.t, a.memberStatus.Status.MemberOperator.RevisionCheck.Conditions, expected...)
	return a
}

func (a *MemberStatusAssertion) HasHostConditionErrorMsg(expected string) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.Len(a.t, a.memberStatus.Status.Host.Conditions, 1)
	require.Equal(a.t, expected, a.memberStatus.Status.Host.Conditions[0].Message)
	return a
}

type AddUsage func(map[string]int)

func OfNodeRole(role string, usage int) AddUsage {
	return func(nodesUsage map[string]int) {
		nodesUsage[role] = usage
	}
}

func (a *MemberStatusAssertion) HasMemoryUsage(usages ...AddUsage) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	expectedUsage := map[string]int{}
	for _, addUsage := range usages {
		addUsage(expectedUsage)
	}
	if len(usages) > 0 {
		assert.Equal(a.t, expectedUsage, a.memberStatus.Status.ResourceUsage.MemoryUsagePerNodeRole)
	} else {
		assert.Empty(a.t, a.memberStatus.Status.ResourceUsage.MemoryUsagePerNodeRole)
	}
	return a
}

func (a *MemberStatusAssertion) HasRoutes(consoleURL string, expCondition toolchainv1alpha1.Condition) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.NotNil(a.t, a.memberStatus.Status.Routes)
	require.Equal(a.t, consoleURL, a.memberStatus.Status.Routes.ConsoleURL)
	test.AssertConditionsMatch(a.t, a.memberStatus.Status.Routes.Conditions, expCondition)
	return a
}

func ComponentsReady() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.ToolchainStatusAllComponentsReadyReason,
	}
}

func ComponentsNotReady(components ...string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.ToolchainStatusComponentsNotReadyReason,
		Message: fmt.Sprintf("components not ready: %v", components),
	}
}

func ConditionReady(reason string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: reason,
	}
}

func ConditionReadyWithMessage(reason, message string) toolchainv1alpha1.Condition { // nolint:unparam
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionTrue,
		Reason:  reason,
		Message: message,
	}
}

func ConditionNotReady(reason, message string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}
}
