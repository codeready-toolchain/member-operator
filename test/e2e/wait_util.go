package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 60
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func waitForUser(t *testing.T, client client.Client, name string) error {
	return wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		user := &userv1.User{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: name}, user); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("waiting for availability of user '%s'\n", name)
				return false, nil
			}
			return false, err
		}
		if user.Name != "" {
			t.Logf("found user '%s'\n", name)
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
				t.Logf("waiting for availability of identity '%s'\n", name)
				return false, nil
			}
			return false, err
		}
		if identity.Name != "" {
			t.Logf("found identity '%s'\n", name)
			return true, nil
		}
		return false, nil
	})
}

func waitForMapping(t *testing.T, client client.Client, userName, identityName string) error {
	return wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		identity := &userv1.UserIdentityMapping{}
		if err := client.Get(context.TODO(), types.NamespacedName{Name: identityName}, identity); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("waiting for availability of identity '%s'\n", identityName)
				return false, nil
			}
			return false, err
		}
		if identity.Name != "" {
			if identity.User.Name == userName {
				t.Logf("found mapping between user '%s' and identity '%s'\n", userName, identityName)
				return true, nil
			}
		}
		return false, nil
	})
}
