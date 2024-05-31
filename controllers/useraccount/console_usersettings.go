package useraccount

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ConsoleUserSettingsIdentifier         = "console.openshift.io/user-settings"
	ConsoleUserSettingsUID                = "console.openshift.io/user-settings-uid"
	UserSettingNS                         = "openshift-console-user-settings"
	ConsoleUserSettingsResourceNamePrefix = "user-settings-"
)

// deleteConfigMap deletes a ConfigMap associated with a user from console setting.
// It first attempts to find the ConfigMap by name. If not found, it then looks for the ConfigMap by label.
// If multiple ConfigMaps are found by label, all of them are deleted.
// The function returns an error if any occurred.
func deleteConfigMap(ctx context.Context, cl client.Client, userUID string) error {
	name := ConsoleUserSettingsResourceNamePrefix + userUID
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("deleting configmap with name %s", name))
	var toDelete []client.Object

	// Create a ConfigMap object type to use with getConsoleUserSettingObjectByName function
	objectType := &corev1.ConfigMap{TypeMeta: metav1.TypeMeta{Kind: "ConfigMap"}}

	// Attempt to find the ConfigMap by name
	configMap, err := getConsoleUserSettingObjectByName(ctx, cl, name, objectType)
	if err != nil {
		return err
	}
	if configMap == nil {
		// could not find the CM by name, try finding the configmap by label
		if configMapList, err := getConfigMapByLabel(ctx, cl, userUID); err != nil || len(configMapList) == 0 {
			return err
		} else if len(configMapList) > 0 {
			// if the number of items retrieved are more than one, should delete all of them
			for key := range configMapList {
				toDelete = append(toDelete, &configMapList[key])
			}
		}
	} else {
		toDelete = append(toDelete, configMap)
	}

	if err := deleteResources(ctx, cl, toDelete); err != nil {
		return err
	}
	return nil
}

// deleteRole deletes a Role associated with a user from console setting.
// It first attempts to find the Role by name. If not found, it then looks for the Role by label.
// If multiple Roles are found by label, all of them are deleted.
// The function returns an error if any occurred.
func deleteRole(ctx context.Context, cl client.Client, userUID string) error {
	name := ConsoleUserSettingsResourceNamePrefix + userUID
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("deleting role with name %s", name))

	var toDelete []client.Object
	objectType := &v1.Role{TypeMeta: metav1.TypeMeta{Kind: "Role"}}

	role, err := getConsoleUserSettingObjectByName(ctx, cl, name, objectType)
	if err != nil {
		return err
	}
	if role == nil {
		// could not find the role by name, try finding the role by label
		if roleList, err := getRolesByLabel(ctx, cl, userUID); err != nil || len(roleList) == 0 {
			return err
		} else if len(roleList) > 0 {
			for key := range roleList {
				toDelete = append(toDelete, &roleList[key])
			}
		}
	} else {
		toDelete = append(toDelete, role)
	}
	if err := deleteResources(ctx, cl, toDelete); err != nil {
		return err
	}
	return nil
}

// deleteRoleBinding deletes a Rolebinding associated with a user from console setting.
// It first attempts to find the Rolebinding by name. If not found, it then looks for the Rolebinding by label.
// If multiple Rolebindings are found by label, all of them are deleted.
// The function returns an error if any occurred.
func deleteRoleBinding(ctx context.Context, cl client.Client, userUID string) error {
	name := ConsoleUserSettingsResourceNamePrefix + userUID
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("deleting rolebinding with name %s", name))

	var toDelete []client.Object
	objectType := &v1.RoleBinding{TypeMeta: metav1.TypeMeta{Kind: "RoleBinding"}}

	rb, err := getConsoleUserSettingObjectByName(ctx, cl, name, objectType)
	if err != nil {
		return err
	}
	if rb == nil {
		// try with label
		if rbList, err := getRoleBindingsByLabel(ctx, cl, userUID); err != nil || len(rbList) == 0 {
			return err
		} else if len(rbList) > 0 {
			for key := range rbList {
				toDelete = append(toDelete, &rbList[key])
			}
		}
	} else {
		toDelete = append(toDelete, rb)
	}
	if err := deleteResources(ctx, cl, toDelete); err != nil {
		return err
	}
	return nil
}

func getConfigMapByLabel(ctx context.Context, cl client.Client, userUID string) ([]corev1.ConfigMap, error) {
	configMapList := &corev1.ConfigMapList{}
	labels := map[string]string{ConsoleUserSettingsIdentifier: "true", ConsoleUserSettingsUID: userUID}
	err := cl.List(ctx, configMapList, client.MatchingLabels(labels), client.InNamespace(UserSettingNS))
	if err != nil {
		return []corev1.ConfigMap{}, err
	}
	return configMapList.Items, nil
}

func getRolesByLabel(ctx context.Context, cl client.Client, userUID string) ([]v1.Role, error) {
	roleList := &v1.RoleList{}
	labels := map[string]string{ConsoleUserSettingsIdentifier: "true", ConsoleUserSettingsUID: userUID}
	err := cl.List(ctx, roleList, client.MatchingLabels(labels), client.InNamespace(UserSettingNS))
	if err != nil {
		return []v1.Role{}, err
	}
	return roleList.Items, nil
}

func getRoleBindingsByLabel(ctx context.Context, cl client.Client, userUID string) ([]v1.RoleBinding, error) {
	rbList := &v1.RoleBindingList{}
	labels := map[string]string{ConsoleUserSettingsIdentifier: "true", ConsoleUserSettingsUID: userUID}
	err := cl.List(ctx, rbList, client.MatchingLabels(labels), client.InNamespace(UserSettingNS))
	if err != nil {
		return []v1.RoleBinding{}, err
	}
	return rbList.Items, nil
}

// function getConsoleUserSettingObjectByName looks for resources of type ConfigMap, Role and Rolebinding in the namespace openshift-console-user-settings by name.
// it returns obj,nil when the object is found by name
// return nil, nil if the object is not found, i.e ignore the not found error
// return nil, err if there is any other kind of error, or if the object type is not supported
func getConsoleUserSettingObjectByName(ctx context.Context, cl client.Client, name string, object client.Object) (client.Object, error) {
	switch object.GetObjectKind().GroupVersionKind().Kind {
	case "Role":
		role := &v1.Role{}
		err := cl.Get(ctx, types.NamespacedName{Namespace: UserSettingNS, Name: name}, role)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return role, nil
	case "RoleBinding":
		rb := &v1.RoleBinding{}
		err := cl.Get(ctx, types.NamespacedName{Namespace: UserSettingNS, Name: name}, rb)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return rb, nil
	case "ConfigMap":
		configMap := &corev1.ConfigMap{}
		err := cl.Get(ctx, types.NamespacedName{Namespace: UserSettingNS, Name: name}, configMap)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return configMap, nil
	}
	return nil, fmt.Errorf(fmt.Sprintf("object type %s is not a console setting supported object", object.GetObjectKind().GroupVersionKind().Kind))
}

func deleteResources(ctx context.Context, cl client.Client, resources []client.Object) error {
	for _, resource := range resources {
		if err := cl.Delete(ctx, resource); err != nil {
			return err
		}
	}
	return nil
}
