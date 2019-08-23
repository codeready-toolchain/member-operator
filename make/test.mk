############################################################
#
# Unit Tests (local) 
#
############################################################

.PHONY: test
## runs the tests without coverage and excluding E2E tests
test:
	@echo "running the tests without coverage and excluding E2E tests..."
	$(Q)go test ${V_FLAG} -race $(shell go list ./... | grep -v /test/e2e) -failfast
	
############################################################
#
# Unit Tests (OpenShift CI )
#
############################################################

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
	# Upload coverage to codecov.io. Since we don't run on a supported CI platform (Jenkins, Travis-ci, etc.), 
	# we need to provide the PR metadata explicitely using env vars used coming from https://github.com/openshift/test-infra/blob/master/prow/jobs.md#job-environment-variables
	# 
	# Also: not using the `-F unittests` flag for now as it's temporarily disabled in the codecov UI 
	# (see https://docs.codecov.io/docs/flags#section-flags-in-the-codecov-ui)
	env
ifneq ($(PR_COMMIT), null)
	@echo "uploading test coverage report for pull-request #$(PULL_NUMBER)..."
	bash <(curl -s https://codecov.io/bash) \
		-t $(CODECOV_TOKEN) \
		-f $(COV_DIR)/coverage.txt \
		-C $(PR_COMMIT) \
		-r $(REPO_OWNER)/$(REPO_NAME) \
		-P $(PULL_NUMBER) \
		-Z
else
	@echo "uploading test coverage report after PR was merged..."
	bash <(curl -s https://codecov.io/bash) \
		-t $(CODECOV_TOKEN) \
		-f $(COV_DIR)/coverage.txt \
		-C $(BASE_COMMIT) \
		-r $(REPO_OWNER)/$(REPO_NAME) \
		-Z
endif

CODECOV_TOKEN := "51cc45ad-2e54-4e68-98cb-8ab15cf2b8df"
REPO_OWNER := $(shell echo $$CLONEREFS_OPTIONS | jq '.refs[0].org')
REPO_NAME := $(shell echo $$CLONEREFS_OPTIONS | jq '.refs[0].repo')
BASE_COMMIT := $(shell echo $$CLONEREFS_OPTIONS | jq '.refs[0].base_sha')
PR_COMMIT := $(shell echo $$CLONEREFS_OPTIONS | jq '.refs[0].pulls[0].sha')
PULL_NUMBER := $(shell echo $$CLONEREFS_OPTIONS | jq '.refs[0].pulls[0].number')

MEMBER_NS := member-operator-$(shell date +'%s')
HOST_NS := host-operator-$(shell date +'%s')

###########################################################
#
# End-to-end Tests
#
###########################################################

.PHONY: test-e2e-keep-namespaces
test-e2e-keep-namespaces: deploy-host e2e-setup setup-kubefed e2e-run

.PHONY: test-e2e
test-e2e: test-e2e-keep-namespaces e2e-cleanup

.PHONY: e2e-run
e2e-run:
	oc get kubefedcluster -n $(MEMBER_NS)
	oc get kubefedcluster -n $(HOST_NS)
	HOST_NS=${HOST_NS} operator-sdk test local ./test/e2e --no-setup --namespace $(MEMBER_NS) --verbose --go-test-flags "-timeout=15m" || \
	($(MAKE) print-logs HOST_NS=${HOST_NS} MEMBER_NS=${MEMBER_NS} && exit 1)

.PHONY: print-logs
print-logs:
	@echo "=====================================================================================" &
	@echo "================================ Host cluster logs =================================="
	@echo "====================================================================================="
	@oc logs deployment.apps/host-operator --namespace $(HOST_NS)
	@echo "====================================================================================="
	@echo "================================ Member cluster logs ================================"
	@echo "====================================================================================="
	@oc logs deployment.apps/member-operator --namespace $(MEMBER_NS)
	@echo "====================================================================================="

.PHONY: e2e-setup
e2e-setup: is-minishift
	oc new-project $(MEMBER_NS) --display-name e2e-tests
	oc apply -f ./deploy/service_account.yaml
	oc apply -f ./deploy/role.yaml
	cat ./deploy/role_binding.yaml | sed s/\REPLACE_NAMESPACE/$(MEMBER_NS)/ | oc apply -f -
	oc apply -f deploy/crds
	sed -e 's|REPLACE_IMAGE|${IMAGE_NAME}|g' ./deploy/operator.yaml  | oc apply -f -

.PHONY: setup-kubefed
setup-kubefed:
	curl -sSL https://raw.githubusercontent.com/codeready-toolchain/toolchain-common/master/scripts/add-cluster.sh | bash -s -- -t member -mn $(MEMBER_NS) -hn $(HOST_NS) -s
	curl -sSL https://raw.githubusercontent.com/codeready-toolchain/toolchain-common/master/scripts/add-cluster.sh | bash -s -- -t host -mn $(MEMBER_NS) -hn $(HOST_NS) -s

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
	oc delete project ${HOST_NS} ${MEMBER_NS} --wait=false || true

.PHONY: clean-e2e-namespaces
clean-e2e-namespaces:
	$(Q)-oc get projects --output=name | grep -E "(member|host)\-operator\-[0-9]+" | xargs oc delete


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
	git clone https://github.com/codeready-toolchain/host-operator.git --depth 1 /tmp/host-operator
	oc new-project $(HOST_NS)
	oc apply -f /tmp/host-operator/deploy/service_account.yaml
	oc apply -f /tmp/host-operator/deploy/role.yaml
	cat /tmp/host-operator/deploy/role_binding.yaml | sed s/\REPLACE_NAMESPACE/$(HOST_NS)/ | oc apply -f -
	oc apply -f /tmp/host-operator/deploy/crds
	sed -e 's|REPLACE_IMAGE|registry.svc.ci.openshift.org/codeready-toolchain/host-operator-v0.1:host-operator|g' /tmp/host-operator/deploy/operator.yaml  | oc apply -f -
