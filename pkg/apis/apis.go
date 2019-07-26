package apis

import (
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	AddToSchemes = append(AddToSchemes, userv1.AddToScheme)
	AddToSchemes = append(AddToSchemes, templatev1.AddToScheme)
	AddToSchemes = append(AddToSchemes, projectv1.AddToScheme)

	return AddToSchemes.AddToScheme(s)
}
