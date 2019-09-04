module github.com/codeready-toolchain/member-operator

require (
	cloud.google.com/go v0.40.0 // indirect
	github.com/Azure/go-autorest v13.0.0+incompatible // indirect
	github.com/codeready-toolchain/api v0.0.0-20190812113906-bd1f09d19c28
	github.com/codeready-toolchain/toolchain-common v0.0.0-20190827045201-35e4f49da4c9
	github.com/go-logr/logr v0.1.0
	github.com/go-openapi/spec v0.19.2 // indirect
	github.com/gobuffalo/envy v1.7.0 // indirect
	github.com/mailru/easyjson v0.0.0-20190620125010-da37f6c1e481 // indirect
	github.com/openshift/api v3.9.0+incompatible
	github.com/operator-framework/operator-sdk v0.10.0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/common v0.6.0 // indirect
	github.com/prometheus/procfs v0.0.3 // indirect
	github.com/redhat-cop/operator-utils v0.0.0-20190530184149-66ee667a40b2
	github.com/satori/go.uuid v1.2.0
	github.com/spf13/pflag v1.0.3
	github.com/stretchr/testify v1.3.0
	golang.org/x/crypto v0.0.0-20190621222207-cc06ce4a13d4 // indirect
	golang.org/x/sys v0.0.0-20190624142023-c5567b49c5d0 // indirect
	golang.org/x/tools v0.0.0-20190625160430-252024b82959 // indirect
	k8s.io/api v0.0.0-20190626000116-b178a738ed00
	k8s.io/apiextensions-apiserver v0.0.0-20190624090600-dfe76d39a269 // indirect
	k8s.io/apimachinery v0.0.0-20190624085041-961b39a1baa0
	k8s.io/apiserver v0.0.0-20190111033246-d50e9ac5404f // indirect
	k8s.io/client-go v11.0.0+incompatible
	k8s.io/code-generator v0.0.0-20190620073620-d55040311883
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a
	k8s.io/klog v0.3.3
	k8s.io/kube-openapi v0.0.0-20190603182131-db7b694dc208
	sigs.k8s.io/controller-runtime v0.1.12
	sigs.k8s.io/controller-tools v0.1.12
	sigs.k8s.io/kubefed v0.1.0-rc2
)

// fix for unavailable git.apache.org https://status.apache.org/incidents/63030p4241xj
replace git.apache.org/thrift.git => github.com/apache/thrift v0.12.0

replace (
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.29.0
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.1.10
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.1.11-0.20190411181648-9d55346c2bde
)

// Pinned to kubernetes-1.13.4
replace (
	k8s.io/api => k8s.io/api v0.0.0-20190222213804-5cb15d344471
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20190228180357-d002e88f6236
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190221213512-86fb29eff628
	k8s.io/apiserver => k8s.io/apiserver v0.0.0-20190228174905-79427f02047f
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20190228180923-a9e421a79326
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190228174230-b40b2a5939e4
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20181117043124-c2090bec4d9b
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20190228175259-3e0149950b0e
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20180711000925-0cf8f7e6ed1d
	k8s.io/kubernetes => k8s.io/kubernetes v1.13.4
)
