ifndef DEV_MK
DEV_MK:=# Prevent repeated "-include".

include ./make/verbose.mk
include ./make/git.mk

DOCKER_REPO?=quay.io/codeready-toolchain
IMAGE_NAME?=member-operator

TIMESTAMP:=$(shell date +%s)
TAG?=$(GIT_COMMIT_ID_SHORT)-$(TIMESTAMP)

# to watch all namespaces, keep namespace empty
APP_NAMESPACE ?= ""
LOCAL_TEST_NAMESPACE ?= "test-saas-control-plane"

.PHONY: up-local
## Run Operator locally
local: login-as-admin create-namespace deploy-rbac build deploy-crd
	$(Q)-oc new-project $(LOCAL_TEST_NAMESPACE) || true
	$(Q)OPERATOR_NAMESPACE=$(LOCAL_TEST_NAMESPACE) operator-sdk up local --namespace=$(APP_NAMESPACE) --verbose

.PHONY: login-as-admin
## Log in as system:admin
login-as-admin:
	$(Q)-echo "Logging using system:admin..."
	$(Q)-oc login -u system:admin

.PHONY: create-namespace
## Create the test namespace
create-namespace:
	$(Q)-echo "Creating Namespace"
	$(Q)-oc new-project $(LOCAL_TEST_NAMESPACE)
	$(Q)-echo "Switching to the namespace $(LOCAL_TEST_NAMESPACE)"
	$(Q)-oc project $(LOCAL_TEST_NAMESPACE)

.PHONY: use-namespace
## Log in as system:admin and enter the test namespace
use-namespace: login-as-admin
	$(Q)-echo "Using to the namespace $(LOCAL_TEST_NAMESPACE)"
	$(Q)-oc project $(LOCAL_TEST_NAMESPACE)

.PHONY: clean-namespace
## Delete the test namespace
clean-namespace:
	$(Q)-echo "Deleting Namespace"
	$(Q)-oc delete project $(LOCAL_TEST_NAMESPACE)

.PHONY: reset-namespace
## Delete an create the test namespace and deploy rbac there
reset-namespace: login-as-admin clean-namespace create-namespace deploy-rbac

.PHONY: deploy-rbac
## Setup service account and deploy RBAC
deploy-rbac:
	$(Q)-oc apply -f deploy/service_account.yaml
	$(Q)-oc apply -f deploy/role.yaml
	$(Q)-oc apply -f deploy/role_binding.yaml

.PHONY: deploy-crd
## Deploy CRD
deploy-crd:
	$(Q)-oc apply -f deploy/crds/toolchain_v1alpha1_nstemplateset_crd.yaml
	$(Q)-oc apply -f deploy/crds/toolchain_v1alpha1_useraccount_crd.yaml
	$(Q)-oc apply -f deploy/crds/core_v1beta1_kubefedcluster.yaml

.PHONY: add-member-to-host
## Run script ./scripts/add-cluster.sh member member-cluster
add-member-to-host:
	@./scripts/add-cluster.sh member member-cluster

.PHONY: add-host-to-member
## Run script ./scripts/add-cluster.sh host host-cluster
add-host-to-member:
	@./scripts/add-cluster.sh host host-cluster

endif
