package useraccount

import (
	"reflect"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
)

func compareNSTemplateSet(first, second toolchainv1alpha1.NSTemplateSetSpec) bool {
	if first.TierName != second.TierName {
		return false
	}
	return compareNamespaces(first.Namespaces, second.Namespaces)
}

func compareNamespaces(namespaces1, namespaces2 []toolchainv1alpha1.Namespace) bool {
	if len(namespaces1) != len(namespaces2) {
		return false
	}
	for _, ns1 := range namespaces1 {
		found := findNamespace(ns1, namespaces2)
		if !found {
			return false
		}
	}
	return true
}

func findNamespace(thisNs toolchainv1alpha1.Namespace, namespaces []toolchainv1alpha1.Namespace) bool {
	for _, ns := range namespaces {
		if reflect.DeepEqual(thisNs, ns) {
			return true
		}
	}
	return false
}
