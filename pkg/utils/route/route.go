package route

import (
	"context"
	"fmt"
	"strings"

	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetRouteURL gets the URL of the route with the given name and namespace using the given client
func GetRouteURL(ctx context.Context, cl client.Client, namespace, name string) (string, error) {
	route := &routev1.Route{}
	namespacedName := types.NamespacedName{Namespace: namespace, Name: name}
	err := cl.Get(ctx, namespacedName, route)
	if err != nil {
		return "", err
	}
	scheme := "https"
	if route.Spec.TLS == nil || *route.Spec.TLS == (routev1.TLSConfig{}) {
		scheme = "http"
	}

	path := route.Spec.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return fmt.Sprintf("%s://%s%s", scheme, route.Spec.Host, path), nil
}
