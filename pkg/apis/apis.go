package apis

import (
	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	AddToSchemes = append(AddToSchemes, userv1.AddToScheme)

	return AddToSchemes.AddToScheme(s)
}
