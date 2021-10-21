.PHONY: lint
## Runs linters on Go code files and YAML files
lint: lint-go-code lint-yaml

YAML_FILES := $(shell find . -type f -regex ".*y[a]ml" -print)
.PHONY: lint-yaml
## runs yamllint on all yaml files
lint-yaml: ${YAML_FILES}
ifeq (, $(shell which yamllint))
	$(error "yamllint not found in PATH. Please install it using instructions on https://yamllint.readthedocs.io/en/stable/quickstart.html#installing-yamllint")
endif
	$(Q)yamllint -c .yamllint $(YAML_FILES)

.PHONY: lint-go-code
## Checks the code with golangci-lint
lint-go-code: install-golangci-lint
	# run with the same flags as on GitHub Actions 
	$(Q)${GOPATH}/bin/golangci-lint ${V_FLAG} run --config=./.golangci.yml --verbose ./...

.PHONY: install-golangci-lint
## Install development tools.
install-golangci-lint:
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin v1.42.1