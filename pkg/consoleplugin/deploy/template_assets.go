// Code generated for package deploy by go-bindata DO NOT EDIT. (@generated)
// sources:
// deploy/consoleplugin/member-operator-console-plugin.yaml
package deploy

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

var _memberOperatorConsolePluginYaml = []byte(`apiVersion: template.openshift.io/v1
kind: Template
metadata:
  name: member-operator-console-plugin
objects:
- apiVersion: v1
  kind: ServiceAccount
  metadata:
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
    name: member-operator-console-plugin
    namespace: ${NAMESPACE}
- kind: Role
  apiVersion: rbac.authorization.k8s.io/v1
  metadata:
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
    name: member-operator-console-plugin
    namespace: ${NAMESPACE}
  rules:
    - apiGroups:
        - toolchain.dev.openshift.com
      resources:
        - memberoperatorconfigs
      verbs:
        - get
        - list
        - watch
    - apiGroups:
        - ""
      resources:
        - secrets
      verbs:
        - get
        - list
        - watch
- kind: RoleBinding
  apiVersion: rbac.authorization.k8s.io/v1
  metadata:
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
    name: member-operator-console-plugin
    namespace: ${NAMESPACE}
  subjects:
    - kind: ServiceAccount
      name: member-operator-console-plugin
  roleRef:
    kind: Role
    name: member-operator-console-plugin
    apiGroup: rbac.authorization.k8s.io
- kind: Deployment
  apiVersion: apps/v1
  metadata:
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
    name: member-operator-console-plugin
    namespace: ${NAMESPACE}
  spec:
    replicas: ${{REPLICAS}}
    selector:
      matchLabels:
        name: member-operator-console-plugin
    template:
      metadata:
        labels:
          name: member-operator-console-plugin
          run: member-operator-console-plugin
      spec:
        serviceAccountName: member-operator-console-plugin
        containers:
        - name: member-operator-console-plugin
          image: ${IMAGE}
          ports:
            - containerPort: 9443
          command:
            - member-operator-console-plugin
          imagePullPolicy: IfNotPresent
          livenessProbe:
            failureThreshold: 3
            httpGet:
              path: /status
              port: 9443
              scheme: HTTPS
            initialDelaySeconds: 1
            periodSeconds: 10
            successThreshold: 1
            timeoutSeconds: 1
          readinessProbe:
            failureThreshold: 30
            httpGet:
              path: /status
              port: 9443
              scheme: HTTPS
            initialDelaySeconds: 1
            periodSeconds: 1
            successThreshold: 1
            timeoutSeconds: 1
          startupProbe:
            failureThreshold: 180
            httpGet:
              path: /status
              port: 9443
              scheme: HTTPS
            initialDelaySeconds: 1
            periodSeconds: 1
            successThreshold: 1
            timeoutSeconds: 1
          env:
            - name: WATCH_NAMESPACE
              value: ${NAMESPACE}
          resources:
            requests:
              cpu: "50m"
              memory: "10M"
          volumeMounts:
          - name: consoleplugin-certs
            mountPath: /etc/consoleplugin/certs
            readOnly: true
        volumes:
        - name: consoleplugin-certs
          secret:
            secretName: member-operator-console-plugin
- kind: Service
  apiVersion: v1
  metadata:
    name: member-operator-console-plugin
    namespace: ${NAMESPACE}
    annotations:
      service.beta.openshift.io/serving-cert-secret-name: member-operator-console-plugin
    labels:
      toolchain.dev.openshift.com/provider: codeready-toolchain
      run: member-operator-console-plugin
  spec:
    ports:
      - port: 9443
        name: "9443"
        targetPort: 9443
    selector:
      run: member-operator-console-plugin
parameters:
  - name: NAMESPACE
    value: 'toolchain-member-operator'
  - name: IMAGE
    value: quay.io/openshiftio/codeready-toolchain/member-operator-console-plugin:latest
  - name: REPLICAS
    value: '3'`)

func memberOperatorConsolePluginYamlBytes() ([]byte, error) {
	return _memberOperatorConsolePluginYaml, nil
}

func memberOperatorConsolePluginYaml() (*asset, error) {
	bytes, err := memberOperatorConsolePluginYamlBytes()
	if err != nil {
		return nil, err
	}

	info := bindataFileInfo{name: "member-operator-console-plugin.yaml", size: 3983, mode: os.FileMode(420), modTime: time.Unix(1710950540, 0)}
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
	"member-operator-console-plugin.yaml": memberOperatorConsolePluginYaml,
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
	"member-operator-console-plugin.yaml": &bintree{memberOperatorConsolePluginYaml, map[string]*bintree{}},
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
