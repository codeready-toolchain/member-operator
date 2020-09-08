#!/bin/bash

set -ex

QUAY_NAMESPACE=${QUAY_NAMESPACE:-codeready-toolchain}
IMAGE_BUILDER=${IMAGE_BUILDER:-docker}
INDEX_IMAGE_URL=${INDEX_IMAGE:-quay.io/${QUAY_NAMESPACE}/toolchain-member-operator-index:latest}
LATEST_INDEX_IMAGE="quay.io/${QUAY_NAMESPACE}/toolchain-member-operator-index:latest"

make build
make docker-push IGNORE_DIRTY=true
make generate-cd-release-manifests QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp FIRST_RELEASE=true
make push-bundle-and-index-image QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp IMAGE_BUILDER=${IMAGE_BUILDER} INDEX_IMAGE_URL=${INDEX_IMAGE_URL} INDEX_PER_COMMIT=true CHANNEL_NAME=staging #FROM_INDEX_URL=${LATEST_INDEX_IMAGE}
make push-bundle-and-index-image QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp IMAGE_BUILDER=${IMAGE_BUILDER} INDEX_IMAGE_URL=${LATEST_INDEX_IMAGE} CHANNEL_NAME=staging #FROM_INDEX_URL=${LATEST_INDEX_IMAGE}
