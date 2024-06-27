# By default the project should be build under GOPATH/src/github.com/<orgname>/<reponame>
GO_PACKAGE_ORG_NAME ?= codeready-toolchain
GO_PACKAGE_REPO_NAME ?= $(shell basename $$PWD)
GO_PACKAGE_PATH ?= github.com/${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}

GO111MODULE?=on
export GO111MODULE
goarch ?= $(shell go env GOARCH)

.PHONY: build
## Build the operator
build: generate-assets $(OUT_DIR)/operator

$(OUT_DIR)/operator:
	$(Q)CGO_ENABLED=0 GOARCH=${goarch} GOOS=linux \
		go build ${V_FLAG} \
		-ldflags "-X ${GO_PACKAGE_PATH}/version.Commit=${GIT_COMMIT_ID} -X ${GO_PACKAGE_PATH}/version.BuildTime=${BUILD_TIME}" \
		-o $(OUT_DIR)/bin/member-operator \
		main.go
	$(Q)CGO_ENABLED=0 GOARCH=${goarch} GOOS=linux \
		go build ${V_FLAG} \
		-ldflags "-X ${GO_PACKAGE_PATH}/version.Commit=${GIT_COMMIT_ID} -X ${GO_PACKAGE_PATH}/version.BuildTime=${BUILD_TIME}" \
		-o $(OUT_DIR)/bin/member-operator-webhook \
		cmd/webhook/main.go
	$(Q)CGO_ENABLED=0 GOARCH=${goarch} GOOS=linux \
		go build ${V_FLAG} \
		-ldflags "-X ${GO_PACKAGE_PATH}/version.Commit=${GIT_COMMIT_ID} -X ${GO_PACKAGE_PATH}/version.BuildTime=${BUILD_TIME}" \
		-o $(OUT_DIR)/bin/member-operator-console-plugin \
		cmd/consoleplugin/main.go

.PHONY: vendor
vendor:
	$(Q)go mod vendor

.PHONY: generate-assets
generate-assets: go-bindata
	@echo "generating webhooks template data..."
	@rm ./pkg/webhook/deploy/webhooks/template_assets.go 2>/dev/null || true
	@$(GO_BINDATA) -pkg webhooks -o ./pkg/webhook/deploy/webhooks/template_assets.go -nocompress -prefix deploy/webhook deploy/webhook
	@echo "generating autoscaler buffer template data..."
	@rm ./pkg/autoscaler/template_assets.go 2>/dev/null || true
	@$(GO_BINDATA) -pkg autoscaler -o ./pkg/autoscaler/template_assets.go -nocompress -prefix deploy/autoscaler deploy/autoscaler
	@echo "generating console plugin template data..."
	@rm ./pkg/consoleplugin/deploy/template_assets.go 2>/dev/null || true
	@$(GO_BINDATA) -pkg deploy -o ./pkg/consoleplugin/deploy/template_assets.go -nocompress -prefix deploy/consoleplugin deploy/consoleplugin

.PHONY: verify-dependencies
## Runs commands to verify after the updated dependecies of toolchain-common/API(go mod replace), if the repo needs any changes to be made
verify-dependencies: tidy vet build test lint-go-code

.PHONY: tidy
tidy: 
	go mod tidy

.PHONY: vet
vet:
	go vet ./...
