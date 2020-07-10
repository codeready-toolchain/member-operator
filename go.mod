module github.com/codeready-toolchain/member-operator

require (
	github.com/codeready-toolchain/api v0.0.0-20200729153019-3cff798dcf50
	github.com/codeready-toolchain/toolchain-common v0.0.0-20200727132335-45a8b65706a7
	github.com/go-logr/logr v0.1.0
	github.com/gofrs/uuid v3.2.0+incompatible
	github.com/openshift/api v3.9.1-0.20190924102528-32369d4db2ad+incompatible
	github.com/operator-framework/operator-sdk v0.17.1
	github.com/pkg/errors v0.9.1
	github.com/redhat-cop/operator-utils v0.0.0-20190827162636-51e6b0c32776
	github.com/satori/go.uuid v1.2.0
	github.com/spf13/cast v1.3.0
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.4.0
	github.com/stretchr/testify v1.4.0
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.17.4
	k8s.io/apiextensions-apiserver v0.17.4
	k8s.io/apimachinery v0.17.4
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.5.2
)

replace github.com/codeready-toolchain/toolchain-common => github.com/MatousJobanek/toolchain-common v0.0.0-20200804092049-b1806c3fe973

replace github.com/codeready-toolchain/api => github.com/MatousJobanek/api v0.0.0-20200803150704-20052fba4e3f

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.0.0+incompatible
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200414152312-3e8f22fb0b56 // Using 'github.com/openshift/api@release-4.4'
	k8s.io/client-go => k8s.io/client-go v0.17.4 // Required by prometheus-operator
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20200204173128-addea2498afe // avoids case-insensitive import collision: "github.com/googleapis/gnostic/openapiv2" and "github.com/googleapis/gnostic/OpenAPIv2"
)

go 1.13
