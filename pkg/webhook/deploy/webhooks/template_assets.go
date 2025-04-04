// Code generated for package webhooks by go-bindata DO NOT EDIT. (@generated)
// sources:
// deploy/webhook/member-operator-webhook.yaml
package webhooks

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)
type asset struct {
	bytes []byte
	info  os.FileInfo
}

type bindataFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Name return file name
func (fi bindataFileInfo) Name() string {
	return fi.name
}

// Size return file size
func (fi bindataFileInfo) Size() int64 {
	return fi.size
}

// Mode return file mode
func (fi bindataFileInfo) Mode() os.FileMode {
	return fi.mode
}

// Mode return file modify time
func (fi bindataFileInfo) ModTime() time.Time {
	return fi.modTime
}

// IsDir return file whether a directory
func (fi bindataFileInfo) IsDir() bool {
	return fi.mode&os.ModeDir != 0
}

// Sys return file is sys mode
func (fi bindataFileInfo) Sys() interface{} {
	return nil
}

var _memberOperatorWebhookYaml = []byte(`kind: Template
apiVersion: template.openshift.io/v1
metadata:
  name: member-operator-webhook
objects:
- apiVersion: scheduling.k8s.io/v1
  kind: PriorityClass
  metadata:
    name: sandbox-users-pods
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
    annotations:
      toolchain.dev.openshift.com/no-deletion: ""
  value: -3
  globalDefault: false
  description: "Priority class for pods in users' namespaces"
- apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRole
  metadata:
    creationTimestamp: null
    name: webhook-role-${NAMESPACE}
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
  rules:
    - apiGroups:
        - ""
      resources:
        - secrets
      verbs:
        - get
        - list
        - watch
    - apiGroups:
        - user.openshift.io
      resources:
        - identities
        - useridentitymappings
        - users
      verbs:
        - get
        - list
        - watch
    - apiGroups:
      - toolchain.dev.openshift.com
      resources:
      - memberoperatorconfigs
      - spacebindingrequests
      verbs:
      - get
      - list
      - watch
    - apiGroups:
      - kubevirt.io
      resources:
      - virtualmachines
      verbs:
      - get
      - list
      - watch
- apiVersion: v1
  kind: ServiceAccount
  metadata:
    name: member-operator-webhook-sa
    namespace: ${NAMESPACE}
- apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRoleBinding
  metadata:
    name: webhook-rolebinding-${NAMESPACE}
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: webhook-role-${NAMESPACE}
  subjects:
    - kind: ServiceAccount
      name: member-operator-webhook-sa
      namespace: ${NAMESPACE}
- apiVersion: v1
  kind: Service
  metadata:
    name: member-operator-webhook
    namespace: ${NAMESPACE}
    labels:
      app: member-operator-webhook
      toolchain.dev.openshift.com/provider: codeready-toolchain
  spec:
    ports:
    - port: 443
      targetPort: 8443
    selector:
      app: member-operator-webhook
- apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: member-operator-webhook
    namespace: ${NAMESPACE}
    labels:
      app: member-operator-webhook
      toolchain.dev.openshift.com/provider: codeready-toolchain
  spec:
    replicas: 1
    selector:
      matchLabels:
        app: member-operator-webhook
    template:
      metadata:
        name: member-operator-webhook
        labels:
          app: member-operator-webhook
      spec:
        serviceAccountName: member-operator-webhook-sa
        containers:
        - name: mutator
          image: ${IMAGE}
          command:
          - member-operator-webhook
          imagePullPolicy: IfNotPresent
          env:
          - name: WATCH_NAMESPACE
            valueFrom:
              fieldRef:
                fieldPath: metadata.namespace
          resources:
            requests:
              cpu: 75m
              memory: 128Mi
          volumeMounts:
          - name: webhook-certs
            mountPath: /etc/webhook/certs
            readOnly: true
        volumes:
        - name: webhook-certs
          secret:
            secretName: webhook-certs
- apiVersion: admissionregistration.k8s.io/v1
  kind: MutatingWebhookConfiguration
  metadata:
    name: member-operator-webhook-${NAMESPACE}
    labels:
      app: member-operator-webhook
      toolchain.dev.openshift.com/provider: codeready-toolchain
  webhooks:
  - name: users.pods.webhook.sandbox
    admissionReviewVersions:
      - v1
    clientConfig:
      caBundle: ${CA_BUNDLE}
      service:
        name: member-operator-webhook
        namespace: ${NAMESPACE}
        path: "/mutate-users-pods"
        port: 443
    matchPolicy: Equivalent
    rules:
    - operations: ["CREATE"]
      apiGroups: [""]
      apiVersions: ["v1"]
      resources: ["pods"]
      scope: "Namespaced"
    sideEffects: None
    timeoutSeconds: 5
    reinvocationPolicy: Never
    failurePolicy: Ignore
    namespaceSelector:
      matchLabels:
        toolchain.dev.openshift.com/provider: codeready-toolchain
  # The users.virtualmachines.webhook.sandbox webhook sets resource limits on VirtualMachines prior to creation as a workaround for https://issues.redhat.com/browse/CNV-28746 (https://issues.redhat.com/browse/CNV-32069)
  # This webhook should be updated to remove the workaround once https://issues.redhat.com/browse/CNV-32069 is complete.
  # The webhook code is available at member-operator/pkg/webhook/mutatingwebhook/vm_mutate.go
  - name: users.virtualmachines.webhook.sandbox
    admissionReviewVersions:
      - v1
    clientConfig:
      caBundle: ${CA_BUNDLE}
      service:
        name: member-operator-webhook
        namespace: ${NAMESPACE}
        path: "/mutate-virtual-machines"
        port: 443
    matchPolicy: Equivalent
    rules:
    - operations: ["CREATE"]
      apiGroups: ["kubevirt.io"]
      apiVersions: ["*"]
      resources: ["virtualmachines"]
      scope: "Namespaced"
    sideEffects: None
    timeoutSeconds: 5
    reinvocationPolicy: Never
    failurePolicy: Fail
    namespaceSelector:
      matchLabels:
        toolchain.dev.openshift.com/provider: codeready-toolchain
- apiVersion: admissionregistration.k8s.io/v1
  kind: ValidatingWebhookConfiguration
  metadata:
    name: member-operator-validating-webhook-${NAMESPACE}
    labels:
      app: member-operator-webhook
      toolchain.dev.openshift.com/provider: codeready-toolchain
  webhooks:
    - name: users.rolebindings.webhook.sandbox
      admissionReviewVersions:
        - v1
      clientConfig:
        caBundle: ${CA_BUNDLE}
        service:
          name: member-operator-webhook
          namespace: ${NAMESPACE}
          path: "/validate-users-rolebindings"
          port: 443
      matchPolicy: Equivalent
      rules:
        - operations: ["CREATE", "UPDATE"]
          apiGroups: ["rbac.authorization.k8s.io","authorization.openshift.io"]
          apiVersions: ["v1"]
          resources: ["rolebindings"]
          scope: "Namespaced"
      sideEffects: None
      timeoutSeconds: 5
      reinvocationPolicy: Never
      failurePolicy: Ignore
      namespaceSelector:
        matchLabels:
          toolchain.dev.openshift.com/provider: codeready-toolchain
    # The users.spacebindingrequests.webhook.sandbox webhook validates SpaceBindingRequest CRs,
    # Specifically it makes sure that once a SBR resource is created, the SpaceBindingRequest.Spec.MasterUserRecord field is not changed by the user.
    # The reason for making SpaceBindingRequest.Spec.MasterUserRecord field immutable is that as of now the SpaceBinding resource name is composed as follows: <Space.Name>-checksum(<Space.Name>-<MasterUserRecord.Name>),
    # thus changing it will trigger an updated of the SpaceBinding content but the name will still be based on the old MUR name.
    # The webhook code is available at member-operator/pkg/webhook/validatingwebhook/validate_spacebindingrequest.go
    - name: users.spacebindingrequests.webhook.sandbox
      admissionReviewVersions:
        - v1
      clientConfig:
        caBundle: ${CA_BUNDLE}
        service:
          name: member-operator-webhook
          namespace: ${NAMESPACE}
          path: "/validate-spacebindingrequests"
          port: 443
      matchPolicy: Equivalent
      rules:
        - operations: ["CREATE", "UPDATE"]
          apiGroups: ["toolchain.dev.openshift.com"]
          apiVersions: ["v1alpha1"]
          resources: ["spacebindingrequests"]
          scope: "Namespaced"
      sideEffects: None
      timeoutSeconds: 5
      reinvocationPolicy: Never
      failurePolicy: Fail
      namespaceSelector:
        matchLabels:
          toolchain.dev.openshift.com/provider: codeready-toolchain
    # The users.virtualmachines.ssp.webhook.sandbox webhook validates SSP CRs,
    # Specifically it blocks the creation/update of SSP resources by sandbox users because it should only be managed by the Virtualization operator
    # The webhook code is available at member-operator/pkg/webhook/validatingwebhook/validate_ssp_request.go
    - name: users.virtualmachines.ssp.webhook.sandbox
      admissionReviewVersions:
        - v1
      clientConfig:
        caBundle: ${CA_BUNDLE}
        service:
          name: member-operator-webhook
          namespace: ${NAMESPACE}
          path: "/validate-ssprequests"
          port: 443
      matchPolicy: Equivalent
      rules:
        - operations: ["CREATE", "UPDATE"]
          apiGroups: ["ssp.kubevirt.io"]
          apiVersions: ["*"]
          resources: ["ssps"]
          scope: "Namespaced"
      sideEffects: None
      timeoutSeconds: 5
      reinvocationPolicy: Never
      failurePolicy: Fail
      namespaceSelector:
        matchLabels:
          toolchain.dev.openshift.com/provider: codeready-toolchain
    # The users.virtualmachines.validating.webhook.sandbox webhook validates VirtualMachine CRs,
    # Specifically it blocks the creation/update of VirtualMachine resources that have '.spec.RunStrategy' set because it interferes with the Idler.
    # The webhook code is available at member-operator/pkg/webhook/validatingwebhook/validate_vm_request.go
    - name: users.virtualmachines.validating.webhook.sandbox
      admissionReviewVersions:
        - v1
      clientConfig:
        caBundle: ${CA_BUNDLE}
        service:
          name: member-operator-webhook
          namespace: ${NAMESPACE}
          path: "/validate-vmrequests"
          port: 443
      matchPolicy: Equivalent
      rules:
        - operations: ["CREATE", "UPDATE"]
          apiGroups: ["kubevirt.io"]
          apiVersions: ["*"]
          resources: ["virtualmachines"]
          scope: "Namespaced"
      sideEffects: None
      timeoutSeconds: 5
      reinvocationPolicy: Never
      failurePolicy: Fail
      namespaceSelector:
        matchLabels:
          toolchain.dev.openshift.com/provider: codeready-toolchain
parameters:
- name: NAMESPACE
  value: 'toolchain-member-operator'
- name: IMAGE
  required: true
- name: CA_BUNDLE
  required: true`)

func memberOperatorWebhookYamlBytes() ([]byte, error) {
	return _memberOperatorWebhookYaml, nil
}

func memberOperatorWebhookYaml() (*asset, error) {
	bytes, err := memberOperatorWebhookYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "member-operator-webhook.yaml", size: 10086, mode: os.FileMode(420), modTime: time.Unix(1736153036, 0)}
	a := &asset{bytes: bytes, info: info}
	return a, nil
}

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("Asset %s can't read by error: %v", name, err)
		}
		return a.bytes, nil
	}
	return nil, fmt.Errorf("Asset %s not found", name)
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}

// AssetInfo loads and returns the asset info for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func AssetInfo(name string) (os.FileInfo, error) {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	if f, ok := _bindata[cannonicalName]; ok {
		a, err := f()
		if err != nil {
			return nil, fmt.Errorf("AssetInfo %s can't read by error: %v", name, err)
		}
		return a.info, nil
	}
	return nil, fmt.Errorf("AssetInfo %s not found", name)
}

// AssetNames returns the names of the assets.
func AssetNames() []string {
	names := make([]string, 0, len(_bindata))
	for name := range _bindata {
		names = append(names, name)
	}
	return names
}

// _bindata is a table, holding each asset generator, mapped to its name.
var _bindata = map[string]func() (*asset, error){
	"member-operator-webhook.yaml": memberOperatorWebhookYaml,
}

// AssetDir returns the file names below a certain
// directory embedded in the file by go-bindata.
// For example if you run go-bindata on data/... and data contains the
// following hierarchy:
//     data/
//       foo.txt
//       img/
//         a.png
//         b.png
// then AssetDir("data") would return []string{"foo.txt", "img"}
// AssetDir("data/img") would return []string{"a.png", "b.png"}
// AssetDir("foo.txt") and AssetDir("notexist") would return an error
// AssetDir("") will return []string{"data"}.
func AssetDir(name string) ([]string, error) {
	node := _bintree
	if len(name) != 0 {
		cannonicalName := strings.Replace(name, "\\", "/", -1)
		pathList := strings.Split(cannonicalName, "/")
		for _, p := range pathList {
			node = node.Children[p]
			if node == nil {
				return nil, fmt.Errorf("Asset %s not found", name)
			}
		}
	}
	if node.Func != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	rv := make([]string, 0, len(node.Children))
	for childName := range node.Children {
		rv = append(rv, childName)
	}
	return rv, nil
}

type bintree struct {
	Func     func() (*asset, error)
	Children map[string]*bintree
}

var _bintree = &bintree{nil, map[string]*bintree{
	"member-operator-webhook.yaml": &bintree{memberOperatorWebhookYaml, map[string]*bintree{}},
}}

// RestoreAsset restores an asset under the given directory
func RestoreAsset(dir, name string) error {
	data, err := Asset(name)
	if err != nil {
		return err
	}
	info, err := AssetInfo(name)
	if err != nil {
		return err
	}
	err = os.MkdirAll(_filePath(dir, filepath.Dir(name)), os.FileMode(0755))
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(_filePath(dir, name), data, info.Mode())
	if err != nil {
		return err
	}
	err = os.Chtimes(_filePath(dir, name), info.ModTime(), info.ModTime())
	if err != nil {
		return err
	}
	return nil
}

// RestoreAssets restores an asset under the given directory recursively
func RestoreAssets(dir, name string) error {
	children, err := AssetDir(name)
	// File
	if err != nil {
		return RestoreAsset(dir, name)
	}
	// Dir
	for _, child := range children {
		err = RestoreAssets(dir, filepath.Join(name, child))
		if err != nil {
			return err
		}
	}
	return nil
}

func _filePath(dir, name string) string {
	cannonicalName := strings.Replace(name, "\\", "/", -1)
	return filepath.Join(append([]string{dir}, strings.Split(cannonicalName, "/")...)...)
}
