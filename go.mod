module github.com/codeready-toolchain/member-operator

require (
	github.com/codeready-toolchain/api v0.0.0-20201014170554-ba7a98533167
	github.com/codeready-toolchain/toolchain-common v0.0.0-20201014231429-a44cccb4b2b5
	github.com/go-logr/logr v0.1.0
	github.com/gofrs/uuid v3.3.0+incompatible
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/openshift/api v3.9.1-0.20190924102528-32369d4db2ad+incompatible
	github.com/operator-framework/operator-sdk v0.19.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1 // indirect
	github.com/redhat-cop/operator-utils v0.0.0-20190827162636-51e6b0c32776
	github.com/satori/go.uuid v1.2.0
	github.com/spf13/cast v1.3.1
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.6.1
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.18.3
	k8s.io/apiextensions-apiserver v0.18.3
	k8s.io/apimachinery v0.18.3
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/metrics v0.18.2
	sigs.k8s.io/controller-runtime v0.6.0
)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200821140346-b94c46af3f2b // Using 'github.com/openshift/api@release-4.5'
	k8s.io/client-go => k8s.io/client-go v0.18.3 // Required by prometheus-operator
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20200410145947-61e04a5be9a6 // avoids case-insensitive import collision: "github.com/googleapis/gnostic/openapiv2" and "github.com/googleapis/gnostic/OpenAPIv2"
)

go 1.14
