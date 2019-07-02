.PHONY: docker-image-minishift
## Build the docker image using the 
docker-image-minishift: ./build/Dockerfile build
	$(Q)(eval $$(minishift oc-env)) && \
	(eval $$(minishift docker-env)) && \
	docker build ${Q_FLAG} \
		--build-arg GO_PACKAGE_PATH=${GO_PACKAGE_PATH} \
		--build-arg VERBOSE=${VERBOSE} \
		-f build/Dockerfile \
		-t ${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT} \
		. \
	
.PHONY: docker-image
## Build the docker image that can be deployed (only contains bare operator)
docker-image: ./build/Dockerfile build
	$(Q)docker build ${Q_FLAG} \
		--build-arg GO_PACKAGE_PATH=${GO_PACKAGE_PATH} \
		--build-arg VERBOSE=${VERBOSE} \
		-f build/Dockerfile \
		-t ${GO_PACKAGE_ORG_NAME}/${GO_PACKAGE_REPO_NAME}:${GIT_COMMIT_ID_SHORT} \
		. \
	