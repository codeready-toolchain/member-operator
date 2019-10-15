package controller

import (
	"github.com/codeready-toolchain/member-operator/pkg/controller/nstemplateset"
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccount"
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccountstatus"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// addToManagerFuncs is a list of functions to add all Controllers to the Manager
var addToManagerFuncs []func(manager.Manager) error

func init() {
	addToManagerFuncs = append(addToManagerFuncs, useraccount.Add)
	addToManagerFuncs = append(addToManagerFuncs, useraccountstatus.Add)
	addToManagerFuncs = append(addToManagerFuncs, nstemplateset.Add)
}

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range addToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}
	return nil
}
