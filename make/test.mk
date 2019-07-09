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
test-e2e:  e2e-setup
	sed -e 's|REPLACE_IMAGE|${IMAGE_NAME}|g' ./deploy/operator.yaml  | oc apply -f -
	operator-sdk test local ./test/e2e --no-setup --namespace $(TEST_NAMESPACE) --go-test-flags "-v -timeout=15m"

.PHONY: e2e-setup
e2e-setup: e2e-cleanup is-minishift
	oc new-project $(TEST_NAMESPACE) --display-name e2e-tests
	oc apply -f ./deploy/service_account.yaml
	oc apply -f ./deploy/role.yaml
	cat ./deploy/role_binding.yaml | sed s/\REPLACE_NAMESPACE/$(TEST_NAMESPACE)/ | oc apply -f -
	oc apply -f deploy/crds

.PHONY: is-minishift
is-minishift:
ifeq ($(OPENSHIFT_BUILD_NAMESPACE),)
	$(info logging as system:admin")
	$(shell echo "oc login -u system:admin")
	$(eval IMAGE_NAME := docker.io/${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT})
	$(shell docker-image)
else
	$(eval IMAGE_NAME := registry.svc.ci.openshift.org/${OPENSHIFT_BUILD_NAMESPACE}/stable:member-operator)
endif

.PHONY: e2e-cleanup
e2e-cleanup:
	$(eval TEST_NAMESPACE := toolchain-member-operator)
	$(Q)-oc delete project $(TEST_NAMESPACE) --timeout=10s --wait

.PHONY: get-test-namespace
get-test-namespace: $(OUT_DIR)/test-namespace
	$(eval TEST_NAMESPACE := $(shell cat $(OUT_DIR)/test-namespace))

$(OUT_DIR)/test-namespace:
	@echo -n "test-namespace-$(shell uuidgen | tr '[:upper:]' '[:lower:]')" > $(OUT_DIR)/test-namespace
