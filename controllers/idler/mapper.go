package idler

import (
	"context"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MapPodToIdler maps the pod to the idler
func MapPodToIdler(_ context.Context, obj *v1.Pod) []reconcile.Request {
	return []reconcile.Request{{
		// the idler should have the same name as the user's namespace
		NamespacedName: types.NamespacedName{
			Name: obj.GetNamespace(),
		},
	}}
}
