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

############################################################
#
# End-to-end Tests
#
############################################################

.PHONY: test-e2e
## Runs the e2e tests locally
test-e2e: e2e-setup 
	$(info Running E2E test: $@)
ifeq ($(OPENSHIFT_VERSION),3)
	$(Q)oc login -u system:admin
endif
	$(Q)operator-sdk test local ./test/e2e --namespace $(TEST_NAMESPACE) --up-local --go-test-flags "-v -timeout=15m"

e2e-setup: ./vendor get-test-namespace e2e-cleanup docker-image
	$(Q)oc new-project $(TEST_NAMESPACE) --display-name e2e-tests
	$(Q)-oc apply -f ./deploy/service_account.yaml 
	$(Q)-oc apply -f ./deploy/role.yaml 
	$(Q)sed -e "s,REPLACE_NAMESPACE,$(TEST_NAMESPACE)," ./deploy/role_binding.yaml | oc apply -f -
	$(foreach crd_file,$(wildcard deploy/crds/*.yaml), \
		oc apply -f $(crd_file); \
	)
	$(eval OPERATOR_IMAGE_NAME := $(shell minishift openshift registry)/${TEST_NAMESPACE}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT})
	$(Q)docker tag ${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT} $(OPERATOR_IMAGE_NAME)
	$(Q)eval `minishift docker-env` && docker login -u admin -p $(shell oc whoami -t) $(shell minishift openshift registry) && docker push $(OPERATOR_IMAGE_NAME)
	$(Q)sed -e "s,REPLACE_IMAGE,$(OPERATOR_IMAGE_NAME)," ./deploy/operator.yaml | oc apply -f -

e2e-cleanup: get-test-namespace
	$(Q)-oc delete project $(TEST_NAMESPACE) --timeout=10s --wait

get-test-namespace: $(OUT_DIR)/test-namespace
	$(eval TEST_NAMESPACE := $(shell cat $(OUT_DIR)/test-namespace))

$(OUT_DIR)/test-namespace:
	@echo -n "test-namespace-$(shell uuidgen | tr '[:upper:]' '[:lower:]')" > $(OUT_DIR)/test-namespace

