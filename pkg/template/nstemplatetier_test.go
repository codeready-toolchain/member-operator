package template_test

import (
	"testing"

	"github.com/codeready-toolchain/api/pkg/apis"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/template"
	"github.com/codeready-toolchain/toolchain-common/pkg/cluster"

	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/h2non/gock.v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/kubefed/pkg/apis/core/common"
	"sigs.k8s.io/kubefed/pkg/apis/core/v1beta1"
)

func TestGetNSTemplateTier(t *testing.T) {

	// given
	// Setup Scheme for all resources
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)
	logf.SetLogger(zap.Logger())
	kubeFedCluster, secret := newHostCluster("host")
	basicTier := &toolchainv1alpha1.NSTemplateTier{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "toolchain.dev.openshift.com/v1alpha1",
			Kind:       "NSTemplateTier",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "basic",
			Namespace: "toolchain-host-operator",
		},
	}
	cl := fake.NewFakeClient(secret, basicTier)
	service := cluster.KubeFedClusterService{Log: logf.Log, Client: cl}
	service.AddKubeFedCluster(kubeFedCluster)

	t.Run("success", func(t *testing.T) {
		// when
		tmpls, err := template.GetNSTemplates("basic")

		// then
		require.NoError(t, err)
		require.Len(t, tmpls, 3)
	})

	t.Run("failures", func(t *testing.T) {

		t.Run("unknown tier", func(t *testing.T) {
			// when
			_, err := template.GetNSTemplates("unknown")
			// then
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unable to retrieve the NSTemplateTier 'unknown' from 'Host' cluster")
		})
	})

}

func newHostCluster(name string) (*v1beta1.KubeFedCluster, *corev1.Secret) {
	// make sure that the client gets a valid 200 OK response
	// when trying to ping the `/api` endpoint on the fake host cluster
	gock.Observe(gock.DumpRequest)

	gock.New("https://" + name).
		Get("api").
		Reply(200).
		BodyString(`{
			"kind": "APIVersions",
			"versions": [
			  "v1"
			]
		  }`)
	gock.New("https://" + name).
		Get("api/v1").
		Reply(200).
		BodyString(`{
			"kind": "APIResourceList",
			"groupVersion": "v1",
			"resources": [
			]
		  }`)
	gock.New("https://" + name).
		Get("apis").
		Reply(200).
		BodyString(`{
			"kind": "APIGroupList",
			"apiVersion": "v1",
			"groups": [
			  {
				"name": "toolchain.dev.openshift.com",
				"versions": [
				  {
					"groupVersion": "toolchain.dev.openshift.com/v1alpha1",
					"version": "v1alpha1"
				  }
				],
				"preferredVersion": {
				  "groupVersion": "toolchain.dev.openshift.com/v1alpha1",
				  "version": "v1alpha1"
				}
			  }
			]
		  }`)
	gock.New("https://" + name).
		Get("apis/toolchain.dev.openshift.com/v1alpha1").
		Reply(200).
		BodyString(`{
			"kind": "APIResourceList",
			"apiVersion": "v1",
			"groupVersion": "toolchain.dev.openshift.com/v1alpha1",
			"resources": [
				{
					"name": "nstemplatetiers",
					"singularName": "nstemplatetier",
					"namespaced": true,
					"kind": "NSTemplateTier",
					"verbs": [
						"delete",
						"deletecollection",
						"get",
						"list",
						"patch",
						"create",
						"update",
						"watch"
					]
				},
				{
					"name": "nstemplatetiers/status",
					"singularName": "",
					"namespaced": true,
					"kind": "NSTemplateTier",
					"verbs": [
						"get",
						"patch",
						"update"
					]
				}
			]
		}`)
	gock.New("https://" + name).
		Get("/apis/toolchain.dev.openshift.com/v1alpha1/namespaces/toolchain-host-operator/nstemplatetiers/basic").
		Reply(200).
		BodyString(`{
			"apiVersion": "toolchain.dev.openshift.com/v1alpha1",
			"kind": "NSTemplateTier",
			"metadata": {
				"name": "basic",
				"namespace": "host-operator-1568102314"
			},
			"spec": {
				"namespaces": [
					{
						"revision": "abcdef",
						"template": "{foo}",
						"type": "ide"
					},
					{
						"revision": "1d2f3q",
						"template": "{bar}",
						"type": "cicd"
					},
					{
						"revision": "a34r57",
						"template": "{baz}",
						"type": "stage"
					}
				]
			}
		}`)

	cluster := &v1beta1.KubeFedCluster{
		Spec: v1beta1.KubeFedClusterSpec{
			APIEndpoint: "https://" + name,
			CABundle:    []byte{},
			SecretRef: v1beta1.LocalSecretReference{
				Name: name,
			},
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "host-namespace",
			Labels: map[string]string{
				"type": "host",
			},
		},
		Status: v1beta1.KubeFedClusterStatus{
			Conditions: []v1beta1.ClusterCondition{{
				Type:   common.ClusterReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "host-namespace",
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token": []byte("mysecrettoken"),
		},
	}
	return cluster, secret
}
