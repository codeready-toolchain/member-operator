package che

import (
	"context"
	"fmt"

	crtcfg "github.com/codeready-toolchain/member-operator/pkg/configuration"

	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("che-client")

// DefaultClient is a default implementation of a CheClient
var DefaultClient *Client

// Client is a client for interacting with Che services
type Client struct {
	config     *crtcfg.Config
	k8sClient  client.Client
	tokenCache *tokenCache
}

// InitDefaultCheClient initializes the default Che service instance
func InitDefaultCheClient(cfg *crtcfg.Config, cl client.Client) {
	DefaultClient = &Client{
		config:     cfg,
		k8sClient:  cl,
		tokenCache: newTokenCache(),
	}
}

func getRouteURL(cl client.Client, namespace, name string) (string, error) {
	route := &routev1.Route{}
	namespacedName := types.NamespacedName{Namespace: namespace, Name: name}
	err := cl.Get(context.TODO(), namespacedName, route)
	if err != nil {
		return "", err
	}
	scheme := "https"
	if route.Spec.TLS == nil || *route.Spec.TLS == (routev1.TLSConfig{}) {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, route.Spec.Host, route.Spec.Path), nil
}
