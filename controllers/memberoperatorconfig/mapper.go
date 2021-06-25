package memberoperatorconfig

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type SecretToMemberOperatorConfigMapper struct {
	client client.Client
}

var _ handler.Mapper = SecretToMemberOperatorConfigMapper{}
var mapperLog = ctrl.Log.WithName("SecretToMemberOperatorConfigMapper")

func (m SecretToMemberOperatorConfigMapper) Map(obj handler.MapObject) []reconcile.Request {
	if _, ok := obj.Object.(*corev1.Secret); ok {
		ns, err := k8sutil.GetWatchNamespace()
		if err != nil {
			mapperLog.Error(err, "Could not determine watched namespace")
			return nil
		}

		config := &toolchainv1alpha1.MemberOperatorConfig{}
		if err := m.client.Get(context.TODO(), types.NamespacedName{Namespace: ns, Name: "config"}, config); err != nil {
			mapperLog.Error(err, "Could not get MemberOperatorConfig resource", "name", "config", "namespace", ns)
			return nil
		}
		return []reconcile.Request{{types.NamespacedName{Namespace: ns, Name: "config"}}}
	}
	// the obj was not a Secret
	return []reconcile.Request{}
}
