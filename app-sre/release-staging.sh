#!/bin/bash

set -ex

QUAY_NAMESPACE=${QUAY_NAMESPACE:-codereadytoolchain}
IMAGE_BUILDER=${IMAGE_BUILDER:-docker}

make build
make docker-push IGNORE_DIRTY=true
make generate-cd-release-manifests QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp #FIRST_RELEASE=true
make push-bundle-and-index-image QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp IMAGE_BUILDER=${IMAGE_BUILDER} INDEX_PER_COMMIT=true INDEX_IMAGE_CHANNEL=app-sre-staging
make recover-operator-dir TMP_DIR=${PWD}/tmp