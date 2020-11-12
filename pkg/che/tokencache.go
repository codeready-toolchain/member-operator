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

	crtCfg "github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/pkg/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pkg/errors"
)

const tokenPath = "auth/realms/che/protocol/openid-connect/token"

// tokenCache manages retrieving, caching and renewing the authentication token required for invoking Che APIs
type tokenCache struct {
	sync.RWMutex
	httpClient *http.Client
	token      *TokenSet
}

func newTokenCache() *tokenCache {
	return &tokenCache{
		httpClient: newHTTPClient(),
	}
}

// getToken returns the token needed to use Che user APIs, or an error if there was a problem getting the token
func (tc *tokenCache) getToken(cl client.Client, cfg *crtCfg.Config) (TokenSet, error) {
	defer tc.RUnlock()
	tc.RLock()
	// use the cached credentials if they are still valid
	if !tokenExpired(tc.token) {
		log.Info("Reusing token")
		return *tc.token, nil
	}

	// get the credentials
	user, pass := cfg.GetCheAdminUsername(), cfg.GetCheAdminPassword()
	if user == "" || pass == "" {
		return TokenSet{}, fmt.Errorf("the che admin username and/or password are not configured")
	}

	// get keycloak URL
	cheKeycloakURL, err := getRouteURL(cl, cfg.GetCheNamespace(), cfg.GetCheKeycloakRouteName())
	if err != nil {
		return TokenSet{}, err
	}
	log.Info("Che Keycloak Route", "URL", cheKeycloakURL)

	// get che user token
	if err = tc.obtainNewToken(cheKeycloakURL+tokenPath, user, pass); err != nil {
		return TokenSet{}, err
	}

	return *tc.token, nil
}

// obtainNewToken obtains an access token from the provided authentication URL and returns it, otherwise returns an error
func (tc *tokenCache) obtainNewToken(authURL, user, pass string) error {
	reqData := url.Values{}
	reqData.Set("username", user)
	reqData.Set("password", pass)
	reqData.Set("grant_type", "password")
	reqData.Set("client_id", "che-public")

	log.Info("Obtaining new token", "URL", authURL)
	res, err := tc.httpClient.PostForm(authURL, reqData)
	if err != nil {
		return err
	}

	defer rest.CloseResponse(res)
	if res.StatusCode != http.StatusOK {
		bodyString := rest.ReadBody(res.Body)
		return errors.Errorf("unable to obtain access token for che, Response status: %s. Response body: %s", res.Status, bodyString)
	}
	tokenSet, err := readTokenSet(res)
	if err != nil {
		return err
	}
	if tokenSet.AccessToken == "" {
		return errors.New("unable to obtain access token for che. Access Token is missing in the response")
	}
	if tokenSet.Expiration == 0 && tokenSet.ExpiresIn > 0 {
		timeLeft := time.Duration(tokenSet.ExpiresIn) * time.Second
		tokenSet.Expiration = time.Now().Add(timeLeft).Unix()
	}
	log.Info("Token expiry", "expiration", tokenSet.Expiration, "expires_in", tokenSet.ExpiresIn)
	tc.token = tokenSet
	return nil
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

// tokenExpired return false if the token is not nil and good for at least one more minute
func tokenExpired(token *TokenSet) bool {
	return token == nil || time.Now().After(time.Unix(token.Expiration-60, 0))
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
