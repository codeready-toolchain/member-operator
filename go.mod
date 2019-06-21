module github.com/codeready-toolchain/member-operator

require (
	contrib.go.opencensus.io/exporter/ocagent v0.4.9 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20170929234023-d6e3b3328b78 // indirect
	github.com/Azure/go-autorest v11.5.2+incompatible // indirect
	github.com/appscode/jsonpatch v0.0.0-20190108182946-7c0e3b262f30 // indirect
	github.com/codeready-toolchain/api v0.0.0-20190612182349-9ac2e55c3c91
	github.com/coreos/bbolt v1.3.1-coreos.6 // indirect
	github.com/coreos/etcd v3.3.13+incompatible // indirect
	github.com/coreos/go-oidc v0.0.0-20180117170138-065b426bd416 // indirect
	github.com/coreos/go-semver v0.0.0-20180108230905-e214231b295a // indirect
	github.com/coreos/go-systemd v0.0.0-20180511133405-39ca1b05acc7 // indirect
	github.com/coreos/pkg v0.0.0-20180108230652-97fdf19511ea // indirect
	github.com/coreos/prometheus-operator v0.26.0 // indirect
	github.com/dgrijalva/jwt-go v3.2.0+incompatible // indirect
	github.com/docker/docker v0.7.3-0.20190327010347-be7ac8be2ae0 // indirect
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/go-logr/logr v0.1.0 // indirect
	github.com/go-logr/zapr v0.1.0 // indirect
	github.com/go-openapi/spec v0.19.2 // indirect
	github.com/golang/mock v1.2.0 // indirect
	github.com/golangci/golangci-lint v1.17.1 // indirect
	github.com/google/btree v0.0.0-20180813153112-4030bb1f1f0c // indirect
	github.com/googleapis/gnostic v0.3.0 // indirect
	github.com/gophercloud/gophercloud v0.0.0-20190318015731-ff9851476e98 // indirect
	github.com/gorilla/websocket v0.0.0-20170926233335-4201258b820c // indirect
	github.com/gregjones/httpcache v0.0.0-20180305231024-9cad4c3443a7 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v0.0.0-20190222133341-cfaf5686ec79 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v0.0.0-20170330212424-2500245aa611 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.8.5 // indirect
	github.com/imdario/mergo v0.3.6 // indirect
	github.com/jonboulle/clockwork v0.0.0-20141017032234-72f9bd7c4e0c // indirect
	github.com/kubernetes-sigs/federation-v2 v0.0.10 // indirect
	github.com/natefinch/lumberjack v2.0.0+incompatible // indirect
	github.com/operator-framework/operator-sdk v0.8.2-0.20190522220659-031d71ef8154
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pquerna/cachecontrol v0.0.0-20171018203845-0dec1b30a021 // indirect
	github.com/soheilhy/cmux v0.1.3 // indirect
	github.com/spf13/pflag v1.0.3
	github.com/tmc/grpc-websocket-proxy v0.0.0-20170815181823-89b8d40f7ca8 // indirect
	github.com/xiang90/probing v0.0.0-20160813154853-07dd2e8dfe18 // indirect
	go.opencensus.io v0.19.2 // indirect
	go.uber.org/atomic v1.3.2 // indirect
	go.uber.org/multierr v1.1.0 // indirect
	go.uber.org/zap v1.9.1 // indirect
	golang.org/x/crypto v0.0.0-20190611184440-5c40567a22f8 // indirect
	golang.org/x/net v0.0.0-20190611141213-3f473d35a33a // indirect
	golang.org/x/sys v0.0.0-20190610200419-93c9922d18ae // indirect
	golang.org/x/time v0.0.0-20180412165947-fbb02b2291d2 // indirect
	golang.org/x/tools v0.0.0-20190612232758-d4e310b4a8a5 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0-20150622162204-20b71e5b60d7 // indirect
	gopkg.in/square/go-jose.v2 v2.0.0-20180411045311-89060dee6a84 // indirect
	gopkg.in/yaml.v1 v1.0.0-20140924161607-9f9df34309c0 // indirect
	gotest.tools v2.2.0+incompatible // indirect
	k8s.io/api v0.0.0-20190612125737-db0771252981 // indirect
	k8s.io/apimachinery v0.0.0-20190612125636-6a5db36e93ad
	k8s.io/apiserver v0.0.0-20190111033246-d50e9ac5404f // indirect
	k8s.io/client-go v2.0.0-alpha.0.0.20181126152608-d082d5923d3c+incompatible
	k8s.io/cluster-registry v0.0.6 // indirect
	k8s.io/code-generator v0.0.0-20190612125529-c522cb6c26aa
	k8s.io/component-base v0.0.0-20190612130303-4062e14deebe // indirect
	k8s.io/gengo v0.0.0-20190327210449-e17681d19d3a
	k8s.io/klog v0.3.2
	k8s.io/kube-openapi v0.0.0-20190603182131-db7b694dc208
	sigs.k8s.io/controller-runtime v0.1.11
	sigs.k8s.io/controller-tools v0.1.10
	sigs.k8s.io/kubefed v0.1.0-rc2
	sigs.k8s.io/structured-merge-diff v0.0.0-20190302045857-e85c7b244fd2 // indirect
	sigs.k8s.io/testing_frameworks v0.1.0 // indirect
)

// Pinned to kubernetes-1.13.1
replace (
	k8s.io/api => k8s.io/api v0.0.0-20181213150558-05914d821849
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20181213153335-0fe22c71c476
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20181127025237-2b1284ed4c93
	k8s.io/client-go => k8s.io/client-go v0.0.0-20181213151034-8d9ed539ba31
)

replace (
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.29.0
	github.com/operator-framework/operator-sdk => github.com/operator-framework/operator-sdk v0.8.1
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20181117043124-c2090bec4d9b
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20180711000925-0cf8f7e6ed1d
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.1.10
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.1.11-0.20190411181648-9d55346c2bde
)
