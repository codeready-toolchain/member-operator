package route

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestGetRouteURL(t *testing.T) {
	// given
	require.NoError(t, routev1.Install(scheme.Scheme))
	ns := "codeready-workspaces-operator"
	name := "keycloak"
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("keycloak-codeready-workspaces-operator.%s", test.MemberClusterName),
			Path: "/",
			TLS: &routev1.TLSConfig{
				Termination: "edge",
			},
		},
	}

	t.Run("with tls and path as slash", func(t *testing.T) {
		// given
		cl := test.NewFakeClient(t, route)

		// when
		routeURL, err := GetRouteURL(context.TODO(), cl, ns, name)

		// then
		require.NoError(t, err)
		assert.Equal(t, "https://keycloak-codeready-workspaces-operator.member-cluster/", routeURL)
	})

	t.Run("without tls and without path", func(t *testing.T) {
		// given
		r := route.DeepCopy()
		r.Spec.TLS = nil
		r.Spec.Path = ""
		cl := test.NewFakeClient(t, r)

		// when
		routeURL, err := GetRouteURL(context.TODO(), cl, ns, name)

		// then
		require.NoError(t, err)
		assert.Equal(t, "http://keycloak-codeready-workspaces-operator.member-cluster/", routeURL)
	})

	t.Run("wit tls and with longer path that doesn't start with slash", func(t *testing.T) {
		// given
		r := route.DeepCopy()
		r.Spec.TLS = nil
		r.Spec.Path = "cool/path"
		cl := test.NewFakeClient(t, r)

		// when
		routeURL, err := GetRouteURL(context.TODO(), cl, ns, name)

		// then
		require.NoError(t, err)
		assert.Equal(t, "http://keycloak-codeready-workspaces-operator.member-cluster/cool/path", routeURL)
	})

	t.Run("wit tls and with longer path that starts with slash", func(t *testing.T) {
		// given
		r := route.DeepCopy()
		r.Spec.TLS = nil
		r.Spec.Path = "/cool/path"
		cl := test.NewFakeClient(t, r)

		// when
		routeURL, err := GetRouteURL(context.TODO(), cl, ns, name)

		// then
		require.NoError(t, err)
		assert.Equal(t, "http://keycloak-codeready-workspaces-operator.member-cluster/cool/path", routeURL)
	})
}
