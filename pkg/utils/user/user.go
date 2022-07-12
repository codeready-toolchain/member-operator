package user

import (
	"context"

	userv1 "github.com/openshift/api/user/v1"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetUsersByOwnerName gets the user resources by matching owner label.
func GetUsersByOwnerName(cl client.Client, owner string) ([]userv1.User, error) {
	userList := &userv1.UserList{}
	labels := map[string]string{toolchainv1alpha1.OwnerLabelKey: owner}
	err := cl.List(context.TODO(), userList, client.MatchingLabels(labels))
	if err != nil {
		return []userv1.User{}, err
	}

	return userList.Items, nil
}
