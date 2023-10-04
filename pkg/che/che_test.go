package che

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/utils/rest"
	commonconfig "github.com/codeready-toolchain/toolchain-common/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"
	testconfig "github.com/codeready-toolchain/toolchain-common/pkg/test/config"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/stretchr/testify/require"
	"gopkg.in/h2non/gock.v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testCheURL = "https://codeready-codeready-workspaces-operator.member-cluster"
)

func TestUserExists(t *testing.T) {
	// given
	testSecret := newTestSecret()

	t.Run("missing che route", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		exists, err := cheClient.UserExists("test-user")

		// then
		require.EqualError(t, err, `request to find Che user 'test-user' failed: routes.route.openshift.io "codeready" not found`)
		require.False(t, exists)
	})

	t.Run("unexpected error", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(400).
			BodyString(`{"error":"che error"}`)
		exists, err := cheClient.UserExists("test-user")

		// then
		require.EqualError(t, err, `request to find Che user 'test-user' failed, Response status: '400 Bad Request' Body: '{"error":"che error"}'`)
		require.False(t, exists)
	})

	t.Run("user not found", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(404).
			BodyString(`{"error":"che error"}`)
		exists, err := cheClient.UserExists("test-user")

		// then
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("user found", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(200).
			BodyString(`{"userID":"abc123"}`)
		exists, err := cheClient.UserExists("test-user")

		// then
		require.NoError(t, err)
		require.True(t, exists)
	})
}

func TestGetUserIDByUsername(t *testing.T) {
	// given
	testSecret := newTestSecret()

	t.Run("missing che route", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		userID, err := cheClient.GetUserIDByUsername("test-user")

		// then
		require.EqualError(t, err, `unable to get Che user ID for user 'test-user': routes.route.openshift.io "codeready" not found`)
		require.Empty(t, userID)
	})

	t.Run("unexpected error", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserFindPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(400).
			BodyString(`{"error":"che error"}`)
		userID, err := cheClient.GetUserIDByUsername("test-user")

		// then
		require.EqualError(t, err, `unable to get Che user ID for user 'test-user', Response status: '400 Bad Request' Body: '{"error":"che error"}'`)
		require.Empty(t, userID)
	})

	t.Run("user ID parse error", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserFindPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(200)
		userID, err := cheClient.GetUserIDByUsername("test-user")

		// then
		require.EqualError(t, err, `unable to get Che user ID for user 'test-user': error unmarshalling Che user json  : unexpected end of JSON input`)
		require.Empty(t, userID)
	})

	t.Run("bad body", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserFindPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(200).
			BodyString(`{"name":"test-user"}`)
		userID, err := cheClient.GetUserIDByUsername("test-user")

		// then
		require.EqualError(t, err, `unable to get Che user ID for user 'test-user': unable to get che user information: Body: '{"name":"test-user"}'`)
		require.Empty(t, userID)
	})

	t.Run("success", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserFindPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(200).
			BodyString(`{"name":"test-user","id":"abc1234"}`)
		userID, err := cheClient.GetUserIDByUsername("test-user")

		// then
		require.NoError(t, err)
		require.Equal(t, "abc1234", userID)
	})
}

func TestDeleteUser(t *testing.T) {
	// given
	testSecret := newTestSecret()

	t.Run("missing che route", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		err := cheClient.DeleteUser("asdf-hjkl")

		// then
		require.EqualError(t, err, `unable to delete Che user with ID 'asdf-hjkl': routes.route.openshift.io "codeready" not found`)
	})

	t.Run("unexpected error", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Delete(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(400).
			BodyString(`{"error":"che error"}`)
		err := cheClient.DeleteUser("asdf-hjkl")

		// then
		require.EqualError(t, err, `unable to delete Che user with ID 'asdf-hjkl', Response status: '400 Bad Request' Body: '{"error":"che error"}'`)
	})

	t.Run("user not found", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Delete(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(404).
			BodyString(`{"message":"user not found"}`)
		err := cheClient.DeleteUser("asdf-hjkl")

		// then
		require.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Delete(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(204)
		err := cheClient.DeleteUser("asdf-hjkl")

		// then
		require.NoError(t, err)
	})
}

func TestUserAPICheck(t *testing.T) {
	// given
	testSecret := newTestSecret()

	t.Run("missing che admin credentials", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: testTokenCache(),
		}

		// when
		err := cheClient.UserAPICheck()

		// then
		require.EqualError(t, err, `che user API check failed: the che admin username and/or password are not configured`)
	})

	t.Run("missing che route", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		err := cheClient.UserAPICheck()

		// then
		require.EqualError(t, err, `che user API check failed: routes.route.openshift.io "codeready" not found`)
	})

	t.Run("error code", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(400).
			BodyString(`{"error":"che error"}`)
		err := cheClient.UserAPICheck()

		// then
		require.EqualError(t, err, `che user API check failed, Response status: '400 Bad Request' Body: '{"error":"che error"}'`)
	})

	t.Run("success", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: tokenCacheWithValidToken(),
		}

		// when
		defer gock.OffAll()
		gock.New(testCheURL).
			Get(cheUserPath).
			MatchHeader("Authorization", "Bearer abc.123.xyz").
			Persist().
			Reply(200)
		err := cheClient.UserAPICheck()

		// then
		require.NoError(t, err)
	})
}

func TestCheRequest(t *testing.T) {
	// given
	testSecret := newTestSecret()

	t.Run("missing configuration", func(t *testing.T) {
		// given
		cl, _ := prepareClientAndConfig(t, cheRoute(true))
		cheClient := &Client{
			httpClient: http.DefaultClient,
			k8sClient:  cl,
			tokenCache: testTokenCache(),
		}

		// when
		res, err := cheClient.cheRequest(http.MethodGet, "", url.Values{}) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
		defer rest.CloseResponse(res)

		// then
		require.EqualError(t, err, "the che admin username and/or password are not configured")
		require.Nil(t, res)
	})

	t.Run("error scenarios", func(t *testing.T) {
		config := commonconfig.NewMemberOperatorConfigWithReset(t,
			testconfig.Che().
				UserDeletionEnabled(true).
				KeycloakRouteName("keycloak").
				Secret().
				Ref("test-secret").
				CheAdminUsernameKey("che.admin.username").
				CheAdminPasswordKey("che.admin.password"))

		t.Run("no che route", func(t *testing.T) {
			// given
			cl, _ := prepareClientAndConfig(t, testSecret)
			cheClient := &Client{
				httpClient: http.DefaultClient,
				k8sClient:  cl,
				tokenCache: testTokenCache(),
			}

			// when
			res, err := cheClient.cheRequest(http.MethodGet, "", url.Values{}) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
			defer rest.CloseResponse(res)

			// then
			require.EqualError(t, err, `routes.route.openshift.io "codeready" not found`)
			require.Nil(t, res)
		})

		t.Run("no keycloak route", func(t *testing.T) {
			// given
			cl, _ := prepareClientAndConfig(t, testSecret, config, cheRoute(true))
			cheClient := &Client{
				httpClient: http.DefaultClient,
				k8sClient:  cl,
				tokenCache: testTokenCache(),
			}

			// when
			res, err := cheClient.cheRequest(http.MethodGet, "", url.Values{}) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
			defer rest.CloseResponse(res)

			// then
			require.EqualError(t, err, `routes.route.openshift.io "keycloak" not found`)
			require.Nil(t, res)
		})

		t.Run("nil query params", func(t *testing.T) {
			// given
			cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
			cheClient := &Client{
				httpClient: http.DefaultClient,
				k8sClient:  cl,
				tokenCache: tokenCacheWithValidToken(),
			}

			// when
			defer gock.OffAll()
			gock.New(testCheURL).
				Get(cheUserFindPath).
				MatchHeader("Authorization", "Bearer abc.123.xyz").
				Persist().
				Reply(400).
				BodyString(`{"error":"che error"}`)
			res, err := cheClient.cheRequest(http.MethodGet, cheUserFindPath, nil) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
			defer rest.CloseResponse(res)

			// then
			require.NoError(t, err)
			require.Equal(t, 400, res.StatusCode)
		})

		t.Run("che returns error", func(t *testing.T) {
			// given
			cl, _ := prepareClientAndConfig(t, testSecret, cheRoute(true), keycloackRoute(true))
			cheClient := &Client{
				httpClient: http.DefaultClient,
				k8sClient:  cl,
				tokenCache: tokenCacheWithValidToken(),
			}

			// when
			defer gock.OffAll()
			gock.New(testCheURL).
				Get(cheUserFindPath).
				MatchHeader("Authorization", "Bearer abc.123.xyz").
				Persist().
				Reply(400).
				BodyString(`{"error":"che error"}`)
			res, err := cheClient.cheRequest(http.MethodGet, cheUserFindPath, url.Values{}) // nolint:bodyclose // see `defer rest.CloseResponse(res)`
			defer rest.CloseResponse(res)

			// then
			require.NoError(t, err)
			require.Equal(t, 400, res.StatusCode)
		})
	})
}

func tokenCacheWithValidToken() *TokenCache {
	return &TokenCache{
		httpClient: http.DefaultClient,
		token: &TokenSet{
			AccessToken:  "abc.123.xyz",
			Expiration:   time.Now().Add(99 * time.Hour).Unix(),
			ExpiresIn:    99,
			RefreshToken: "111.222.333",
			TokenType:    "bearer",
		},
	}
}

func cheRoute(tls bool) *routev1.Route { //nolint: unparam
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "codeready",
			Namespace: "codeready-workspaces-operator",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("codeready-codeready-workspaces-operator.%s", test.MemberClusterName),
			Path: "/",
		},
	}
	if tls {
		r.Spec.TLS = &routev1.TLSConfig{
			Termination: "edge",
		}
	}
	return r
}

func newTestSecret() *v1.Secret {
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: test.MemberOperatorNs,
		},
		Data: map[string][]byte{
			"che.admin.username": []byte("test-che-user"),
			"che.admin.password": []byte("test-che-password"),
		},
	}
}
