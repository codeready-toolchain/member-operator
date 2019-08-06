
.PHONY: deploy-member
## Deploy Operator on dev cluster
deploy-member: create-namespace deploy-rbac deploy-crd
	oc apply -f deploy/operator_dev_cluster.yaml
