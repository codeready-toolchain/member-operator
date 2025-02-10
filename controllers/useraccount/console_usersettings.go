package useraccount

import (
	"context"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ConsoleUserSettingsIdentifier         = "console.openshift.io/user-settings"
	ConsoleUserSettingsUID                = "console.openshift.io/user-settings-uid"
	UserSettingNS                         = "openshift-console-user-settings"
	ConsoleUserSettingsResourceNamePrefix = "user-settings-"
	ConsoleUserSettingsRoleSuffix         = "-role"
	ConsoleUserSettingsRoleBindingSuffix  = "-rolebinding"
)

// deleteResource deletes the specified resource associated with a user from console setting.
// It attempts to delete the resource by name, and does nothing if not found.
//
// userUID : The unique identifier of the user for whom the resource is being deleted.
// Returns an error if the deletion operation fails. Returns nil if the operation is successful or there is nothing to delete.
func deleteResource(ctx context.Context, cl client.Client, userUID string, toDelete client.Object) error {
	name := ConsoleUserSettingsResourceNamePrefix + userUID
	if toDelete.GetObjectKind().GroupVersionKind().Kind == "Role" {
		name = name + ConsoleUserSettingsRoleSuffix
	} else if toDelete.GetObjectKind().GroupVersionKind().Kind == "RoleBinding" {
		name = name + ConsoleUserSettingsRoleBindingSuffix
	}

	toDelete.SetName(name)
	toDelete.SetNamespace(UserSettingNS)
	if err := cl.Delete(ctx, toDelete); err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
