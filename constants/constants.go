package constants

import "os"

const (
	RBACControllerLabelKey = "rbac-controller.kubernetes.io"
)

var (
	RBACControllerSuffix = ""
)

func init() {
	ns, found := os.LookupEnv("RBAC_CONTROLLER_SUFFIX")
	if found {
		RBACControllerSuffix = ns
	}
}
