package che

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	memberCfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/utils/rest"
	"github.com/codeready-toolchain/member-operator/pkg/utils/route"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pkg/errors"
)

const tokenPath = "auth/realms/codeready/protocol/openid-connect/token"

// TokenCache manages retrieving, caching and renewing the authentication token required for invoking Che APIs
type TokenCache struct {
	sync.RWMutex
	httpClient *http.Client
	token      *TokenSet
}

// NewTokenCache creates a new instance of a TokenCache
func NewTokenCache(cl *http.Client) *TokenCache {
	return NewTokenCacheWithToken(cl, nil)
}

// NewTokenCacheWithToken creates a new instance of a TokenCache
func NewTokenCacheWithToken(cl *http.Client, t *TokenSet) *TokenCache {
	return &TokenCache{
		httpClient: cl,
		token:      t,
	}
}

// getToken returns the token needed to use Che user APIs, or an error if there was a problem getting the token
func (tc *TokenCache) getToken(cl client.Client, cfg memberCfg.Configuration) (TokenSet, error) {
	tc.RLock()
	// use the cached credentials if they are still valid
	if !tokenExpired(tc.token) {
		defer tc.RUnlock()
		log.Info("Reusing token")
		return *tc.token, nil
	}
	tc.RUnlock()

	// token is no good, get a new one and update the cache
	return tc.obtainAndCacheNewToken(cl, cfg)
}

// obtainAndCacheNewToken obtains an access token, updates the cache and returns the token. Returns an error if there was a failure at any point
func (tc *TokenCache) obtainAndCacheNewToken(cl client.Client, cfg memberCfg.Configuration) (TokenSet, error) {
	defer tc.Unlock()
	tc.Lock()

	// do a token check here because if multiple go routines were blocking waiting for the lock above, there may be a newly cached token they can use and can skip obtaining a new one
	if !tokenExpired(tc.token) {
		return *tc.token, nil
	}

	// no valid token, retrieve a new one
	// get the credentials
	user, pass := cfg.Che().AdminUserName(), cfg.Che().AdminPassword()
	if user == "" || pass == "" {
		return TokenSet{}, fmt.Errorf("the che admin username and/or password are not configured")
	}

	// get keycloak URL
	cheKeycloakURL, err := route.GetRouteURL(cl, cfg.Che().Namespace(), cfg.Che().KeycloakRouteName())
	if err != nil {
		return TokenSet{}, err
	}
	log.Info("Che Keycloak Route", "URL", cheKeycloakURL)

	reqData := url.Values{}
	reqData.Set("username", user)
	reqData.Set("password", pass)
	reqData.Set("grant_type", "password")
	reqData.Set("client_id", "codeready-public")

	authURL := cheKeycloakURL + tokenPath
	log.Info("Obtaining new token", "URL", authURL)
	res, err := tc.httpClient.PostForm(authURL, reqData)
	if err != nil {
		return TokenSet{}, err
	}

	defer rest.CloseResponse(res)
	if res.StatusCode != http.StatusOK {
		bodyString, readError := rest.ReadBody(res.Body)
		if readError != nil {
			log.Error(readError, "error while reading body of the get token response")
		}
		return TokenSet{}, errors.Errorf("unable to obtain access token for che, Response status: '%s'. Response body: '%s'", res.Status, bodyString)
	}
	tokenSet, err := readTokenSet(res)
	if err != nil {
		return TokenSet{}, err
	}
	if tokenSet.AccessToken == "" {
		return TokenSet{}, errors.New("unable to obtain access token for che. Access Token is missing in the response")
	}
	if tokenSet.Expiration == 0 && tokenSet.ExpiresIn > 0 {
		timeLeft := time.Duration(tokenSet.ExpiresIn) * time.Second
		tokenSet.Expiration = time.Now().Add(timeLeft).Unix()
	}
	log.Info("Token expiry", "expiration", tokenSet.Expiration, "expires_in", tokenSet.ExpiresIn)

	// update token cache
	tc.token = tokenSet
	return *tc.token, nil
}

// TokenSet represents a set of Access and Refresh tokens
type TokenSet struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Expiration   int64  `json:"expiration"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

// readTokenSet extracts json with token data from the response
func readTokenSet(res *http.Response) (*TokenSet, error) {
	buf := new(bytes.Buffer)
	_, err := io.Copy(buf, res.Body)
	if err != nil {
		return nil, err
	}
	jsonString := strings.TrimSpace(buf.String())
	return readTokenSetFromJSON(jsonString)
}

// readTokenSetFromJSON parses json with a token set
func readTokenSetFromJSON(jsonString string) (*TokenSet, error) {
	var token TokenSet
	err := json.Unmarshal([]byte(jsonString), &token)
	if err != nil {
		return nil, errors.Wrapf(err, "error when unmarshal json with access token %s ", jsonString)
	}
	return &token, nil
}

// tokenExpired return false if the token is not nil and good for at least thirty more seconds
func tokenExpired(token *TokenSet) bool {
	return token == nil || time.Now().After(time.Unix(token.Expiration-30, 0))
}

// newHTTPClient returns a new HTTP client with some timeout and TLS values configured
func newHTTPClient() *http.Client {
	var netTransport = &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}
	var httpClient = &http.Client{
		Timeout:   time.Second * 10,
		Transport: netTransport,
	}
	return httpClient
}
