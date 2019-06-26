package controller

import (
	"github.com/codeready-toolchain/member-operator/pkg/controller/useraccountstatus"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, useraccountstatus.Add)
}
