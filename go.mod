module github.com/codeready-toolchain/member-operator

require (
	github.com/RHEcosystemAppEng/dbaas-operator v0.1.4-0.20220222181358-34f519992531
	github.com/codeready-toolchain/api v0.0.0-20220304110911-0727a0adecdc
	github.com/codeready-toolchain/toolchain-common v0.0.0-20220307143744-81636a33ef62
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.6
	// using latest commit from 'github.com/openshift/api@release-4.9'
	github.com/openshift/api v0.0.0-20211028023115-7224b732cc14
	github.com/pkg/errors v0.9.1
	github.com/redhat-cop/operator-utils v1.3.3-0.20220121120056-862ef22b8cdf
	github.com/satori/go.uuid v1.2.0
	github.com/stretchr/testify v1.7.0
	go.uber.org/zap v1.19.0
	gopkg.in/h2non/gock.v1 v1.0.14
	k8s.io/api v0.22.7
	k8s.io/apiextensions-apiserver v0.22.7
	k8s.io/apimachinery v0.22.7
	k8s.io/client-go v0.22.7
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.9.0
	k8s.io/metrics v0.22.7
	sigs.k8s.io/controller-runtime v0.10.3
)

go 1.16
