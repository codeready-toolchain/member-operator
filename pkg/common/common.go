package common

import (
	"fmt"

	"github.com/codeready-toolchain/member-operator/pkg/config"
)

func ToIdentityName(userID string) string {
	return fmt.Sprintf("%s:%s", config.GetIdP(), userID)
}
