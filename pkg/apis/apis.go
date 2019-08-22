package apis

import (
	"github.com/codeready-toolchain/api/pkg/apis"
	projectv1 "github.com/openshift/api/project/v1"
	userv1 "github.com/openshift/api/user/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add openshift specific resource
	addToSchemes := append(apis.AddToSchemes, userv1.AddToScheme)
	addToSchemes = append(addToSchemes, projectv1.AddToScheme)

	return addToSchemes.AddToScheme(s)
}
