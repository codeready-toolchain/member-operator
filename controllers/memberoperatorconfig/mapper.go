package memberoperatorconfig

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var mapperLog = ctrl.Log.WithName("SecretToMemberOperatorConfigMapper")

// MapSecretToMemberOperatorConfig maps secrets to the singular instance of MemberOperatorConfig named "config"
func MapSecretToMemberOperatorConfig() func(object client.Object) []reconcile.Request {
	return func(obj client.Object) []reconcile.Request {
		if secret, ok := obj.(*corev1.Secret); ok {
			mapperLog.Info("Secret mapped to MemberOperatorConfig", "name", secret.Name)
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: secret.Namespace, Name: "config"}}}
		}
		// the obj was not a Secret
		return []reconcile.Request{}
	}
}
