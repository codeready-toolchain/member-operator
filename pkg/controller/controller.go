package controller

import (
	"github.com/codeready-toolchain/member-operator/pkg/configuration"
	"github.com/codeready-toolchain/member-operator/pkg/controller/memberstatus"
	"github.com/codeready-toolchain/member-operator/pkg/controller/nstemplateset"
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccount"
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccountstatus"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// addToManagerFuncs is a list of functions to add all Controllers to the Manager
var addToManagerFuncs []func(manager.Manager, *configuration.Config) error

func init() {
	addToManagerFuncs = append(addToManagerFuncs, memberstatus.Add)
	addToManagerFuncs = append(addToManagerFuncs, useraccount.Add)
	addToManagerFuncs = append(addToManagerFuncs, useraccountstatus.Add)
	addToManagerFuncs = append(addToManagerFuncs, nstemplateset.Add)
	addToManagerFuncs = append(addToManagerFuncs, idler.Add)
}

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager, config *configuration.Config) error {
	for _, f := range addToManagerFuncs {
		if err := f(m, config); err != nil {
			return err
		}
	}
	return nil
}
