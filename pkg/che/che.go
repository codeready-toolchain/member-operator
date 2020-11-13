package che

import (
	crtcfg "github.com/codeready-toolchain/member-operator/pkg/configuration"

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
