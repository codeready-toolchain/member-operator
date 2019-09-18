package nstemplateset

import "fmt"

var _templatesBasicDevYaml = []byte(`apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    project: codeready-toolchain
  name: basic-dev
objects:
  - apiVersion: v1
    kind: Namespace
    metadata:
      labels:
        provider: codeready-toolchain
        project: codeready-toolchain
      name: ${USER_NAME}-dev
  - apiVersion: authorization.openshift.io/v1
    kind: RoleBinding
    metadata:
      labels:
        provider: codeready-toolchain
        app: codeready-toolchain
      name: user-edit
      namespace: ${USER_NAME}-dev
    roleRef:
      name: edit
    subjects:
      - kind: User
        name: ${USER_NAME}
    userNames:
      - ${USER_NAME}
parameters:
  - name: USER_NAME
    value: johnsmith
`)

var _templatesBasicCodeYaml = []byte(`apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    project: codeready-toolchain
  name: basic-code
objects:
  - apiVersion: v1
    kind: Namespace
    metadata:
      labels:
        provider: codeready-toolchain
        project: codeready-toolchain
      name: ${USER_NAME}-code
  - apiVersion: authorization.openshift.io/v1
    kind: RoleBinding
    metadata:
      labels:
        provider: codeready-toolchain
        app: codeready-toolchain
      name: user-edit
      namespace: ${USER_NAME}-code
    roleRef:
      name: edit
    subjects:
      - kind: User
        name: ${USER_NAME}
    userNames:
      - ${USER_NAME}
parameters:
  - name: USER_NAME
    value: johnsmith
`)

var _templatesBasicStageYaml = []byte(`apiVersion: template.openshift.io/v1
kind: Template
metadata:
  labels:
    provider: codeready-toolchain
    project: codeready-toolchain
  name: basic-stage
objects:
  - apiVersion: v1
    kind: Namespace
    metadata:
      labels:
        provider: codeready-toolchain
        project: codeready-toolchain
      name: ${USER_NAME}-stage
  - apiVersion: authorization.openshift.io/v1
    kind: RoleBinding
    metadata:
      labels:
        provider: codeready-toolchain
        app: codeready-toolchain
      name: user-edit
      namespace: ${USER_NAME}-stage
    roleRef:
      name: edit
    subjects:
      - kind: User
        name: ${USER_NAME}
    userNames:
      - ${USER_NAME}
parameters:
  - name: USER_NAME
    value: johnsmith
`)

func getTemplateContent(tierName, typeName string) ([]byte, error) {
	if tierName == "basic" && typeName == "dev" {
		return _templatesBasicDevYaml, nil
	} else if tierName == "basic" && typeName == "code" {
		return _templatesBasicCodeYaml, nil
	} else if tierName == "basic" && typeName == "stage" {
		return _templatesBasicStageYaml, nil
	}
	return nil, fmt.Errorf("no template found for tier:%s, type:%s", tierName, typeName)
}
