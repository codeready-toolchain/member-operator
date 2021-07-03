package che

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/codeready-toolchain/member-operator/pkg/apis"
	crtcfg "github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/toolchain-common/pkg/test"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/stretchr/testify/require"
	"gopkg.in/h2non/gock.v1"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	testKeycloakURL = "https://keycloak-codeready-workspaces-operator.member-cluster"
)

func prepareClientAndConfig(t *testing.T, initObjs ...runtime.Object) (client.Client, *crtcfg.Config) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	s := scheme.Scheme
	err := apis.AddToScheme(s)
	require.NoError(t, err)

	fakeClient := test.NewFakeClient(t, initObjs...)
	config, err := crtcfg.LoadConfig(fakeClient)
	require.NoError(t, err)

	return fakeClient, config
}

func TestGetToken(t *testing.T) {
	// given
	testSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "toolchain-member",
		},
		Data: map[string][]byte{
			"che.admin.username": []byte("test-che-user"),
			"che.admin.password": []byte("test-che-password"),
		},
	}

	t.Run("missing configuration", func(t *testing.T) {
		// given
		tokenCache := testTokenCache()
		cl, cfg := prepareClientAndConfig(t)

		// when
		tok, err := tokenCache.getToken(cl, cfg)

		// then
		require.EqualError(t, err, "the che admin username and/or password are not configured")
		require.Empty(t, tok)
	})

	t.Run("with configuration", func(t *testing.T) {
		restore := test.SetEnvVarsAndRestore(t,
			test.Env("WATCH_NAMESPACE", "toolchain-member"),
			test.Env("MEMBER_OPERATOR_SECRET_NAME", "test-secret"),
			test.Env(crtcfg.MemberEnvPrefix+"_CHE_KEYCLOAK_ROUTE_NAME", "keycloak"),
		)
		defer restore()

		t.Run("no keycloak route", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			require.EqualError(t, err, `routes.route.openshift.io "keycloak" not found`)
			require.Empty(t, tok)
		})

		t.Run("keycloak route returns error", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(500).
				BodyString(`{"error":"fake error"}`)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			require.EqualError(t, err, `unable to obtain access token for che, Response status: '500 Internal Server Error'. Response body: '{"error":"fake error"}'`)
			require.Empty(t, tok)
		})

		t.Run("expired token received", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			expiredToken := &TokenSet{
				AccessToken:  "abc.123.xyz",
				Expiration:   time.Now().Unix(),
				ExpiresIn:    300,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			tokenCache.token = expiredToken
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(200).
				BodyString(`{
					"access_token":"aaa.bbb.ccc",
					"expires_in":300,
					"refresh_expires_in":1800,
					"refresh_token":"111.222.333",
					"token_type":"bearer",
					"not-before-policy":0,
					"session_state":"a2fa1448-687a-414f-af40-3b6b3f5a873a",
					"scope":"profile email"
					}`)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			expected := TokenSet{
				AccessToken:  "aaa.bbb.ccc",
				ExpiresIn:    300,
				Expiration:   1605165761,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			require.NoError(t, err)
			require.NotEqual(t, expiredToken.Expiration, tok.Expiration) // should receive a new token with new expiry
			expectToken(t, expected, tok)                                // the rest of the token values should be the same
		})

		t.Run("keycloak route returns bad status code", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(404)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			require.EqualError(t, err, "unable to obtain access token for che, Response status: '404 Not Found'. Response body: ''")
			require.Empty(t, tok)
		})

		t.Run("bad token received", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(200).
				BodyString(`error`)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			require.Error(t, err, "there should be an unmarshal error")
			require.Empty(t, tok)
		})

		t.Run("missing access token", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			noAccessToken := &TokenSet{
				Expiration:   time.Now().Unix(),
				ExpiresIn:    300,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			tokenCache.token = noAccessToken
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(200).
				BodyString(`{
					"access_token":"",
					"expires_in":300,
					"refresh_expires_in":1800,
					"refresh_token":"111.222.333",
					"token_type":"bearer",
					"not-before-policy":0,
					"session_state":"a2fa1448-687a-414f-af40-3b6b3f5a873a",
					"scope":"profile email"
					}`)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			require.EqualError(t, err, "unable to obtain access token for che. Access Token is missing in the response")
			require.Empty(t, tok)
		})

		t.Run("new valid token returned", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))
			defer gock.OffAll()
			gock.New(testKeycloakURL).
				Post(tokenPath).
				MatchHeader("Content-Type", "application/x-www-form-urlencoded").
				Persist().
				Reply(200).
				BodyString(`{
					"access_token":"aaa.bbb.ccc",
					"expires_in":300,
					"refresh_expires_in":1800,
					"refresh_token":"111.222.333",
					"token_type":"bearer",
					"not-before-policy":0,
					"session_state":"a2fa1448-687a-414f-af40-3b6b3f5a873a",
					"scope":"profile email"
					}`)

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			expected := TokenSet{
				AccessToken:  "aaa.bbb.ccc",
				ExpiresIn:    300,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			require.NoError(t, err)
			expectToken(t, expected, tok)
		})

		t.Run("token is valid so it is reused", func(t *testing.T) {
			// given
			tokenCache := testTokenCache()
			cl, cfg := prepareClientAndConfig(t, testSecret, keycloackRoute(true))

			expTime := time.Now().Add(120 * time.Second).Unix()
			goodToken := &TokenSet{
				AccessToken:  "abc.123.xyz",
				Expiration:   expTime,
				ExpiresIn:    300,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			tokenCache.token = goodToken

			// when
			tok, err := tokenCache.getToken(cl, cfg)

			// then
			expected := TokenSet{
				AccessToken:  "abc.123.xyz",
				ExpiresIn:    300,
				RefreshToken: "111.222.333",
				TokenType:    "bearer",
			}
			require.NoError(t, err)
			expectToken(t, expected, tok)
			require.Equal(t, expTime, tok.Expiration) // token should be the reused so expiration should be the same
		})

	})

}

func TestTokenExpired(t *testing.T) {
	t.Run("nil token", func(t *testing.T) {
		// given
		var token *TokenSet = nil

		// when
		result := tokenExpired(token)

		// then
		require.True(t, result)
	})

	t.Run("expiration not set", func(t *testing.T) {
		// given
		token := &TokenSet{
			Expiration: 0,
		}

		// when
		result := tokenExpired(token)

		// then
		require.True(t, result)
	})

	t.Run("not expired", func(t *testing.T) {
		// given
		offset := 61 * time.Second
		token := &TokenSet{
			Expiration: time.Now().Add(offset).Unix(),
		}

		// when
		result := tokenExpired(token)

		// then
		require.False(t, result)
	})

	t.Run("expired", func(t *testing.T) {
		// given
		offset := 29 * time.Second
		token := &TokenSet{
			Expiration: time.Now().Add(offset).Unix(),
		}

		// when
		result := tokenExpired(token)

		// then
		require.True(t, result)
	})
}

func keycloackRoute(tls bool) *routev1.Route { //nolint: unparam
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "keycloak",
			Namespace: "codeready-workspaces-operator",
		},
		Spec: routev1.RouteSpec{
			Host: fmt.Sprintf("keycloak-codeready-workspaces-operator.%s", test.MemberClusterName),
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

func expectToken(t *testing.T, expected, actual TokenSet) {
	require.Equal(t, expected.AccessToken, actual.AccessToken)
	require.Equal(t, expected.ExpiresIn, actual.ExpiresIn)
	require.Equal(t, expected.RefreshToken, actual.RefreshToken)
	require.Equal(t, expected.TokenType, actual.TokenType)
}

func testTokenCache() *TokenCache {
	return &TokenCache{
		httpClient: http.DefaultClient,
	}
}
