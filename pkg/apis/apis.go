package apis

import (
	"github.com/codeready-toolchain/api/pkg/apis"

	openshiftappsv1 "github.com/openshift/api/apps/v1"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	quotav1 "github.com/openshift/api/quota/v1"
	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	addToSchemes := append(apis.AddToSchemes, userv1.Install)
	addToSchemes = append(addToSchemes, templatev1.Install)
	addToSchemes = append(addToSchemes, projectv1.Install)
	addToSchemes = append(addToSchemes, authv1.Install)
	addToSchemes = append(addToSchemes, quotav1.Install)
	addToSchemes = append(addToSchemes, extensionsv1.AddToScheme)
	addToSchemes = append(addToSchemes, rbacv1.AddToScheme)
	addToSchemes = append(addToSchemes, corev1.AddToScheme)
	addToSchemes = append(addToSchemes, appsv1.AddToScheme)
	addToSchemes = append(addToSchemes, openshiftappsv1.AddToScheme)

	return addToSchemes.AddToScheme(s)
}
