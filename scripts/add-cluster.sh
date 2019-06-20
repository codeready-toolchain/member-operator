#!/usr/bin/env bash


JOINING_CLUSTER_TYPE=$1
JOINING_CLUSTER_NAME=$2
OPERATOR_NS=${OPERATOR_NAMESPACE:toolchain-member-operator}
SA_NAME=${JOINING_CLUSTER_TYPE}"-operator"

CLUSTER_JOIN_TO="host"
if [[ ${JOINING_CLUSTER_TYPE} == "host" ]]; then
  CLUSTER_JOIN_TO="member"
fi

echo "Switching to profile ${JOINING_CLUSTER_TYPE}"
minishift profile set ${JOINING_CLUSTER_TYPE}
make login-as-admin
oc project "toolchain-${JOINING_CLUSTER_TYPE}-operator"

echo "Getting ${JOINING_CLUSTER_TYPE} SA token"
SA_SECRET=`oc get sa ${SA_NAME} -o json | jq -r .secrets[].name | grep token`
SA_TOKEN=`oc get secret ${SA_SECRET} -o json | jq -r '.data["token"]' | base64 -d`
SA_CA_CRT=`oc get secret ${SA_SECRET} -o json | jq -r '.data["ca.crt"]'`

API_ENDPOINT=`oc config view --raw --minify -o json | jq -r '.clusters[0].cluster["server"]'`

echo "Switching to profile ${CLUSTER_JOIN_TO}"
minishift profile set ${CLUSTER_JOIN_TO}
make login-as-admin
oc project "toolchain-${CLUSTER_JOIN_TO}-operator"

oc create secret generic ${SA_NAME}-${JOINING_CLUSTER_NAME} --from-literal=token="${SA_TOKEN}" --from-literal=ca.crt="${SA_CA_CRT}"

KUBEFEDCLUSTER_CRD="apiVersion: core.kubefed.k8s.io/v1beta1
kind: KubeFedCluster
metadata:
  name: ${JOINING_CLUSTER_NAME}
  namespace: ${OPERATOR_NS}
  labels:
    type: ${JOINING_CLUSTER_TYPE}
spec:
  apiEndpoint: ${API_ENDPOINT}
  caBundle: ${SA_CA_CRT}
  secretRef:
    name: ${SA_NAME}-${JOINING_CLUSTER_NAME}
"

echo "Creating KubeFedCluster representation of ${JOINING_CLUSTER_TYPE} in ${CLUSTER_JOIN_TO}:"
echo ${KUBEFEDCLUSTER_CRD}

cat <<EOF | oc apply -f -
${KUBEFEDCLUSTER_CRD}
EOF
