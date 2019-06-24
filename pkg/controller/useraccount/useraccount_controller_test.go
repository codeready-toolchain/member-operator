package useraccount

import (
	"testing"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/apis"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

func TestEnsureUser(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	s := scheme.Scheme
	apis.AddToScheme(s)

	t.Run("create_user", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient()
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		newUser, created, err := reconciler.ensureUser(userAcc)
		assert.NoError(t, err)
		assert.True(t, created)
		assert.NotNil(t, newUser)
		assert.Equal(t, username, newUser.Name)
	})

	t.Run("get_user", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		user := newUser(userAcc)
		client := fake.NewFakeClient(user)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		newUser, created, err := reconciler.ensureUser(userAcc)
		assert.NoError(t, err)
		assert.False(t, created)
		assert.NotNil(t, newUser)
		assert.Equal(t, userAcc.Name, newUser.Name)
	})
}

func TestEnsureIdentity(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	s := scheme.Scheme
	apis.AddToScheme(s)

	t.Run("create_identity", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		client := fake.NewFakeClient()
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		newIdentity, created, err := reconciler.ensureIdentity(userAcc)
		assert.NoError(t, err)
		assert.True(t, created)
		assert.NotNil(t, newIdentity)
	})

	t.Run("get_identity", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		identity := newIdentity(userAcc)
		client := fake.NewFakeClient(identity)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		newIdentity, created, err := reconciler.ensureIdentity(userAcc)
		assert.NoError(t, err)
		assert.False(t, created)
		assert.NotNil(t, newIdentity)
	})
}

func TestEnsureMapping(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))
	username := "johnsmith"
	userID := "34113f6a-2e0d-11e9-be9a-525400fb443d"
	s := scheme.Scheme
	apis.AddToScheme(s)

	t.Run("create_mapping_true", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		user := newUser(userAcc)
		identity := newIdentity(userAcc)
		client := fake.NewFakeClient(user, identity)
		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}

		created, err := reconciler.ensureMapping(userAcc, user, identity)
		assert.NoError(t, err)
		assert.True(t, created)
	})

	t.Run("create_mapping_false", func(t *testing.T) {
		userAcc := newUserAccount(username, userID)
		user := newUser(userAcc)
		identity := newIdentity(userAcc)
		mapping := newMapping(user, identity)
		client := fake.NewFakeClient(user, identity, mapping)

		reconciler := &ReconcileUserAccount{
			client: client,
			scheme: s,
		}
		created, err := reconciler.ensureMapping(userAcc, user, identity)
		assert.NoError(t, err)
		assert.False(t, created)
	})
}

func newUserAccount(userName, userID string) *toolchainv1alpha1.UserAccount {
	userAcc := &toolchainv1alpha1.UserAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: userName,
		},
		Spec: toolchainv1alpha1.UserAccountSpec{
			UserID: userID,
		},
	}
	return userAcc
}
