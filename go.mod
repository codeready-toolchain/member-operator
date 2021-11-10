module github.com/codeready-toolchain/member-operator

require (
	github.com/codeready-toolchain/api v0.0.0-20210930215026-da4da11421c7
	github.com/codeready-toolchain/toolchain-common v0.0.0-20210928071424-2ace1784e8d2
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.2
	// using latest commit from 'github.com/openshift/api@release-4.7'
	github.com/openshift/api v0.0.0-20210428205234-a8389931bee7
	github.com/pkg/errors v0.9.1
	github.com/redhat-cop/operator-utils v1.1.3-0.20210602122509-2eaf121122d2
	github.com/satori/go.uuid v1.2.0
	github.com/stretchr/testify v1.7.0
	go.uber.org/zap v1.19.0
	golang.org/x/crypto v0.0.0-20201117144127-c1f2f97bffc9 // indirect
	gopkg.in/h2non/gock.v1 v1.0.14
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.8.0
	k8s.io/metrics v0.20.2
	sigs.k8s.io/controller-runtime v0.8.3
)

replace github.com/codeready-toolchain/api => github.com/xcoulon/api v0.0.0-20211110143945-dec71f4e5073

replace github.com/codeready-toolchain/toolchain-common => github.com/xcoulon/toolchain-common v0.0.0-20211110171137-ac3d818a6a9a

go 1.16
