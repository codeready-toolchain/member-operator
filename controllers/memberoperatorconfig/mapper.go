package memberoperatorconfig

import (
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type SecretToMemberOperatorConfigMapper struct{}

var _ handler.Mapper = SecretToMemberOperatorConfigMapper{}
var mapperLog = ctrl.Log.WithName("SecretToMemberOperatorConfigMapper")

// Map maps secrets to the singular instance of MemberOperatorConfig named "config"
func (m SecretToMemberOperatorConfigMapper) Map(obj handler.MapObject) []reconcile.Request {
	if secret, ok := obj.Object.(*corev1.Secret); ok {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: secret.Namespace, Name: "config"}}}
	}
	// the obj was not a Secret
	return []reconcile.Request{}
}
