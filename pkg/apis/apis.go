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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	metrics "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// AddToScheme adds all Resources to the default Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	addToSchemes := append(apis.AddToSchemes, userv1.Install,
		templatev1.Install,
		projectv1.Install,
		authv1.Install,
		quotav1.Install,
		extensionsv1.AddToScheme,
		rbacv1.AddToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
		openshiftappsv1.Install,
		batchv1.AddToScheme,
		metrics.AddToScheme)

	return addToSchemes.AddToScheme(s)
}
