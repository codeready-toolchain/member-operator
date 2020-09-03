#!/bin/bash

set -ex

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
QUAY_NAMESPACE=${QUAY_NAMESPACE:-codeready-toolchain}
IMAGE_BUILDER=${IMAGE_BUILDER:-docker}
INDEX_IMAGE_NAME=${INDEX_IMAGE_NAME:-toolchain-member-operator-index}

make build
make docker-push IGNORE_DIRTY=true
make generate-cd-release-manifests QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp #FIRST_RELEASE=true

CSV_LOCATION="${SCRIPT_DIR}/../deploy/olm-catalog/toolchain-member-operator/manifests/*clusterserviceversion.yaml"
CURRENT_VERSION=`grep "^  version: " ${CSV_LOCATION} | awk '{print $2}'`

make push-bundle-and-index-image QUAY_NAMESPACE=${QUAY_NAMESPACE} TMP_DIR=${PWD}/tmp IMAGE_BUILDER=${IMAGE_BUILDER} INDEX_IMAGE=${INDEX_IMAGE_NAME} INDEX_PER_COMMIT=true CHANNEL_NAME=app-sre-staging
make recover-operator-dir TMP_DIR=${PWD}/tmp

oc process -f ${SCRIPT_DIR}/deploy-operator.yaml \
  -p NAMESPACE=toolchain-member-operator \
  -p QUAY_NAMESPACE=${QUAY_NAMESPACE} \
  -p INDEX_IMAGE_NAME=${INDEX_IMAGE_NAME} \
  -p INDEX_IMAGE_TAG=${CURRENT_VERSION} \
  | oc apply -f -
