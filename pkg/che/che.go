package che

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	membercfg "github.com/codeready-toolchain/member-operator/controllers/memberoperatorconfig"
	"github.com/codeready-toolchain/member-operator/pkg/utils/rest"
	"github.com/codeready-toolchain/member-operator/pkg/utils/route"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	cheUserPath     = "api/user"
	cheUserFindPath = cheUserPath + "/find"
)

var log = logf.Log.WithName("che-client")

// DefaultClient is a default implementation of a CheClient
var DefaultClient *Client

// Client is a client for interacting with Che services
type Client struct {
	httpClient *http.Client
	k8sClient  client.Client
	tokenCache *TokenCache
}

// InitDefaultCheClient initializes the default Che client instance
func InitDefaultCheClient(cl client.Client) {
	defaultHTTPClient := newHTTPClient()
	tc := NewTokenCache(defaultHTTPClient)
	DefaultClient = NewCheClient(defaultHTTPClient, cl, tc)
}

// NewCheClient creates a new instance of a Che client
func NewCheClient(httpCl *http.Client, cl client.Client, tc *TokenCache) *Client {
	return &Client{
		httpClient: httpCl,
		k8sClient:  cl,
		tokenCache: tc,
	}
}

// UserExists returns true if the username exists, false if it doesn't and an error if there was problem with the request
func (c *Client) UserExists(username string) (bool, error) {
	reqData := url.Values{}
	reqData.Set("name", username)
	res, err := c.cheRequest(http.MethodGet, cheUserFindPath, reqData) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
	defer rest.CloseResponse(res)
	if err != nil {
		return false, errors.Wrapf(err, "request to find Che user '%s' failed", username)
	}
	if res.StatusCode == http.StatusOK {
		log.Info("User found", "name", username)
		return true, nil
	} else if res.StatusCode == http.StatusNotFound {
		log.Info("User not found", "name", username)
		return false, nil
	}
	resBody, readError := rest.ReadBody(res.Body)
	if readError != nil {
		log.Error(readError, "error while reading body of the find Che user response")
	}
	return false, errors.Errorf("request to find Che user '%s' failed, Response status: '%s' Body: '%s'", username, res.Status, resBody)
}

// GetUserIDByUsername returns the user ID that maps to the given username or an error if the user was not found or there was a problem with the request
func (c *Client) GetUserIDByUsername(username string) (string, error) {
	reqData := url.Values{}
	reqData.Set("name", username)
	res, err := c.cheRequest(http.MethodGet, cheUserFindPath, reqData)
	defer rest.CloseResponse(res)
	if err != nil {
		return "", errors.Wrapf(err, "unable to get Che user ID for user '%s'", username)
	}
	if res.StatusCode != http.StatusOK {
		resBody, readError := rest.ReadBody(res.Body)
		if readError != nil {
			log.Error(readError, "error while reading body of the get Che user ID response")
		}
		err = errors.Errorf("unable to get Che user ID for user '%s', Response status: '%s' Body: '%s'", username, res.Status, resBody)
		return "", err
	}
	cheUser, err := readCheUser(res)
	if err != nil {
		return "", errors.Wrapf(err, "unable to get Che user ID for user '%s'", username)
	}
	return cheUser.ID, err
}

// DeleteUser deletes the Che user with the given user ID
func (c *Client) DeleteUser(userID string) error {
	log.Info("Deleting user", "userID", userID)
	res, err := c.cheRequest(http.MethodDelete, path.Join(cheUserPath, userID), nil) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
	defer rest.CloseResponse(res)
	if err != nil {
		return errors.Wrapf(err, "unable to delete Che user with ID '%s'", userID)
	}
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusNotFound {
		resBody, readError := rest.ReadBody(res.Body)
		if readError != nil {
			log.Error(readError, "error while reading body of the delete Che user response")
		}
		err = errors.Errorf("unable to delete Che user with ID '%s', Response status: '%s' Body: '%s'", userID, res.Status, resBody)
	} else if res.StatusCode == http.StatusNotFound {
		log.Info("The user was not deleted because it wasn't found", "userID", userID)
	}
	return err
}

// UserAPICheck returns an error if the Che user API cannot be reached successfully, returns nil otherwise
func (c *Client) UserAPICheck() error {
	reqData := url.Values{}
	res, err := c.cheRequest(http.MethodGet, cheUserPath, reqData) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
	defer rest.CloseResponse(res)
	if err != nil {
		return errors.Wrapf(err, "che user API check failed")
	}
	if res.StatusCode == http.StatusOK {
		return nil
	}
	resBody, readError := rest.ReadBody(res.Body)
	if readError != nil {
		log.Error(readError, "error while reading body of the che user API check response")
	}
	return errors.Errorf("che user API check failed, Response status: '%s' Body: '%s'", res.Status, resBody)
}

func (c *Client) cheRequest(method, endpoint string, queryParams url.Values) (*http.Response, error) {

	config, err := membercfg.GetConfiguration(c.k8sClient)
	if err != nil {
		return nil, err
	}

	// get Che route URL
	cheURL, err := route.GetRouteURL(c.k8sClient, config.Che().Namespace(), config.Che().RouteName())
	if err != nil {
		return nil, err
	}

	// create request
	req, err := http.NewRequestWithContext(context.TODO(), method, cheURL+endpoint, nil)
	if err != nil {
		return nil, err
	}

	if queryParams != nil {
		req.URL.RawQuery = queryParams.Encode()
	}

	// get auth token for request
	token, err := c.tokenCache.getToken(c.k8sClient, config)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+token.AccessToken)

	// do the request
	return c.httpClient.Do(req)
}

// User holds the user data retrieved from the Che user API
type User struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// readCheUser extracts json with token data from the response
func readCheUser(res *http.Response) (*User, error) {
	buf := new(bytes.Buffer)
	_, err := io.Copy(buf, res.Body)
	if err != nil {
		return nil, err
	}
	jsonString := strings.TrimSpace(buf.String())
	cheUser, err := readCheUserFromJSON(jsonString)
	if err != nil {
		return nil, err
	}
	if cheUser.ID == "" {
		return nil, errors.Errorf("unable to get che user information: Body: '%s'", jsonString)
	}
	return cheUser, nil
}

// readCheUserFromJSON parses json with a token set
func readCheUserFromJSON(jsonString string) (*User, error) {
	var cheUser User
	err := json.Unmarshal([]byte(jsonString), &cheUser)
	if err != nil {
		return nil, errors.Wrapf(err, "error unmarshalling Che user json %s ", jsonString)
	}
	return &cheUser, nil
}

// DevSpacesDBCleanerDelete deletes the user from the Dev Spaces database via the che-db-cleaner service
// curl -X DELETE http://che-db-cleaner.crw/<userid>
func (c *Client) DevSpacesDBCleanerDelete(userID string) error {

	config, err := membercfg.GetConfiguration(c.k8sClient)
	if err != nil {
		return err
	}

	// Dev Spaces URL
	devSpacesURL := fmt.Sprintf("http://che-db-cleaner.%s/%s", config.Che().Namespace(), userID)

	// create request
	req, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, devSpacesURL, nil)
	if err != nil {
		return errors.Wrapf(err, "unable to create Dev Spaces delete request")
	}

	// do the request
	res, err := c.httpClient.Do(req) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
	defer rest.CloseResponse(res)
	if err != nil {
		return errors.Wrapf(err, "unable to delete Dev Spaces user with ID '%s'", userID)
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
		resBody, readError := rest.ReadBody(res.Body)
		if readError != nil {
			log.Error(readError, "error while reading body of the delete Dev Spaces user response")
		}
		err = errors.Errorf("unable to delete Dev Spaces user with ID '%s', Response status: '%s' Body: '%s'", userID, res.Status, resBody)
	} else if res.StatusCode == http.StatusNotFound {
		log.Info("The Dev Spaces user was not deleted because it wasn't found", "userID", userID)
	}
	return err
}
