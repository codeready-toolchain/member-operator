package test

import (
	"context"
	"fmt"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

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

func (a *MemberStatusAssertion) HasMemberOperatorConditionErrorMsg(msg string) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.Contains(a.t, a.memberStatus.Status.MemberOperator.Conditions[0].Message, msg)
	return a
}

func (a *MemberStatusAssertion) HasHostConnectionConditionErrorMsg(msg string) *MemberStatusAssertion {
	err := a.loadMemberStatus()
	require.NoError(a.t, err)
	require.Contains(a.t, *a.memberStatus.Status.HostConnection.Conditions[0].Message, msg)
	return a
}

func ComponentsReady() toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:   toolchainv1alpha1.ConditionReady,
		Status: corev1.ConditionTrue,
		Reason: toolchainv1alpha1.MemberStatusAllComponentsReady,
	}
}

func ComponentsNotReady(components ...string) toolchainv1alpha1.Condition {
	return toolchainv1alpha1.Condition{
		Type:    toolchainv1alpha1.ConditionReady,
		Status:  corev1.ConditionFalse,
		Reason:  toolchainv1alpha1.MemberStatusComponentsNotReady,
		Message: fmt.Sprintf("components not ready: %v", components),
	}
}
