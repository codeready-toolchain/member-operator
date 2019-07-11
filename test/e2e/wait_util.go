package e2e

import (
	"context"
	"testing"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"

	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	operatorRetryInterval = time.Second * 5
	operatorTimeout       = time.Second * 60
	retryInterval         = time.Millisecond * 100
	timeout               = time.Second * 3
	cleanupRetryInterval  = time.Second * 1
	cleanupTimeout        = time.Second * 5
)

func waitForUser(t *testing.T, client client.Client, name string) error {
	return wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		user := &userv1.User{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: name}, user); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("waiting for availability of user '%s'", name)
				return false, nil
			}
			return false, err
		}
		if user.Name != "" && len(user.Identities) > 0 {
			t.Logf("found user '%s'", name)
			return true, nil
		}
		return false, nil
	})
}

func waitForIdentity(t *testing.T, client client.Client, name string) error {
	return wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		identity := &userv1.Identity{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: name}, identity); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("waiting for availability of identity '%s'", name)
				return false, nil
			}
			return false, err
		}
		if identity.Name != "" && identity.User.Name != "" {
			t.Logf("found identity '%s'", name)
			return true, nil
		}
		return false, nil
	})
}

func waitForUserAccStatusConditions(t *testing.T, client client.Client, namespace, username string, conditions ...toolchainv1alpha1.Condition) error {
	return wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		userAcc := &toolchainv1alpha1.UserAccount{}
		if err := client.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: username}, userAcc); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("waiting for availability of useraccount '%s'", username)
				return false, nil
			}
			return false, err
		}
		if ConditionsMatch(userAcc.Status.Conditions, conditions...) {
			t.Log("conditions match")
			return true, nil
		}
		return false, nil
	})
}

// TODO Move to toolchain-common
func ConditionsMatch(actual []toolchainv1alpha1.Condition, expected ...toolchainv1alpha1.Condition) bool {
	if len(expected) != len(actual) {
		return false
	}
	for _, c := range expected {
		if !ContainsCondition(actual, c) {
			return false
		}
	}
	return true
}

// TODO Move to toolchain-common
func ContainsCondition(conditions []toolchainv1alpha1.Condition, contains toolchainv1alpha1.Condition) bool {
	for _, c := range conditions {
		if c.Type == contains.Type {
			return contains.Status == c.Status && contains.Reason == c.Reason && contains.Message == c.Message
		}
	}
	return false
}
