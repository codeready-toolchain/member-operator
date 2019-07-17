############################################################
#
# (local) Tests
#
############################################################

.PHONY: test
## runs the tests without coverage and excluding E2E tests
test:
	@echo "running the tests without coverage and excluding E2E tests..."
	$(Q)go test ${V_FLAG} -race $(shell go list ./... | grep -v /test/e2e) -failfast
	
############################################################
#
# OpenShift CI Tests
#
############################################################

.PHONY: test-ci
# runs the tests and uploads the coverage report on codecov.io
# DO NOT USE LOCALLY: must only be called by OpenShift CI when processing new PR and when a PR is merged! 
test-ci: test-with-coverage upload-codecov-report

# Output directory for coverage information
COV_DIR = $(OUT_DIR)/coverage

.PHONY: test-with-coverage
## runs the tests with coverage
test-with-coverage: 
	@echo "running the tests with coverage..."
	@-mkdir -p $(COV_DIR)
	@-rm $(COV_DIR)/coverage.txt
	$(Q)go test -vet off ${V_FLAG} $(shell go list ./... | grep -v /test/e2e) -coverprofile=$(COV_DIR)/coverage.txt -covermode=atomic ./...

.PHONY: upload-codecov-report
# Uploads the test coverage reports to codecov.io. 
# DO NOT USE LOCALLY: must only be called by OpenShift CI when processing new PR and when a PR is merged! 
upload-codecov-report: 
	# Upload coverage to codecov.io
	bash <(curl -s https://codecov.io/bash) -f $(COV_DIR)/coverage.txt -t 51cc45ad-2e54-4e68-98cb-8ab15cf2b8df

###########################################################
#
# End-to-end Tests
#
###########################################################

.PHONY: test-e2e
test-e2e:  deploy-host e2e-setup setup-kubefed
	# This is hack to fix https://github.com/operator-framework/operator-sdk/issues/1657
	echo "info: Running go mod vendor"
	go mod vendor
	operator-sdk test local ./test/e2e --no-setup --namespace $(TEST_NAMESPACE) --go-test-flags "-v -timeout=15m"
	# remove me once verified
	oc get kubefedcluster -n $(TEST_NAMESPACE)
	oc get kubefedcluster -n $(HOST_NS)
	oc logs $(oc get pods -o name) -n $(HOST_NS)
	oc logs $(oc get pods -o name) -n $(TEST_NAMESPACE)

.PHONY: e2e-setup
e2e-setup: get-test-namespace is-minishift
	oc new-project $(TEST_NAMESPACE) --display-name e2e-tests
	oc apply -f ./deploy/service_account.yaml
	oc apply -f ./deploy/role.yaml
	cat ./deploy/role_binding.yaml | sed s/\REPLACE_NAMESPACE/$(TEST_NAMESPACE)/ | oc apply -f -
	oc apply -f deploy/crds
	sed -e 's|REPLACE_IMAGE|${IMAGE_NAME}|g' ./deploy/operator.yaml  | oc apply -f -

.PHONY: setup-kubefed
setup-kubefed:
    # TODO update this link which will be pointing to toolchain-common master once merged
	curl -sSL https://gist.githubusercontent.com/dipak-pawar/af5065ef097bfac878b6b567d867f78f/raw/d053a4641ead6f7b4381d72e1bb660827e62f71c/create_fedcluster.sh | bash -s -- -t member -mn $(TEST_NAMESPACE) -hn $(HOST_NS)
	curl -sSL https://gist.githubusercontent.com/dipak-pawar/af5065ef097bfac878b6b567d867f78f/raw/d053a4641ead6f7b4381d72e1bb660827e62f71c/create_fedcluster.sh | bash -s -- -t host -mn $(TEST_NAMESPACE) -hn $(HOST_NS)

.PHONY: is-minishift
is-minishift:
ifeq ($(OPENSHIFT_BUILD_NAMESPACE),)
	$(info logging as system:admin")
	$(shell echo "oc login -u system:admin")
	$(eval IMAGE_NAME := docker.io/${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT})
	$(shell echo "make docker-image")
else
	$(eval IMAGE_NAME := registry.svc.ci.openshift.org/${OPENSHIFT_BUILD_NAMESPACE}/stable:member-operator)
endif

.PHONY: e2e-cleanup
e2e-cleanup:
	$(eval TEST_NAMESPACE := $(shell cat $(OUT_DIR)/test-namespace))
	$(Q)-oc delete project $(TEST_NAMESPACE) --timeout=10s --wait

.PHONY: get-test-namespace
get-test-namespace: $(OUT_DIR)/test-namespace
	$(eval TEST_NAMESPACE := $(shell cat $(OUT_DIR)/test-namespace))

$(OUT_DIR)/test-namespace:
	@echo -n "member-operator-$(shell date +'%s')" > $(OUT_DIR)/test-namespace

###########################################################
#
# Deploying Host Operator in Openshift CI Environment for End to End tests
#
###########################################################

.PHONY: deploy-host
deploy-host:
	$(eval HOST_NS := $(shell echo -n "host-operator-$(shell date +'%s')"))
	rm -rf /tmp/host-operator
	# cloning shallow as don't want to maintain it for every single change in deploy directory of host-operator
	git clone git@github.com:codeready-toolchain/host-operator.git --depth 1 /tmp/host-operator
	oc new-project $(HOST_NS)
	oc apply -f /tmp/host-operator/deploy/service_account.yaml
	oc apply -f /tmp/host-operator/deploy/role.yaml
	cat /tmp/host-operator/deploy/role_binding.yaml | sed s/\REPLACE_NAMESPACE/$(HOST_NS)/ | oc apply -f -
	oc apply -f /tmp/host-operator/deploy/crds
	sed -e 's|REPLACE_IMAGE|registry.svc.ci.openshift.org/codeready-toolchain/host-operator-v0.1:host-operator|g' /tmp/host-operator/deploy/operator.yaml  | oc apply -f -
