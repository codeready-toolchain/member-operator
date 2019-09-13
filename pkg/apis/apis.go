package apis

import (
	"github.com/codeready-toolchain/api/pkg/apis"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	addToSchemes := append(apis.AddToSchemes, userv1.Install)
	addToSchemes = append(addToSchemes, templatev1.Install)
	addToSchemes = append(addToSchemes, projectv1.Install)
	addToSchemes = append(addToSchemes, authv1.Install)

	return addToSchemes.AddToScheme(s)
}
