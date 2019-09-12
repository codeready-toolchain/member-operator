package template

import "k8s.io/apimachinery/pkg/runtime"

var (
	// RetainNamespaces a func to retain only namespaces
	RetainNamespaces FilterFunc = func(obj runtime.RawExtension) bool {
		gvk := obj.Object.GetObjectKind().GroupVersionKind()
		return gvk.Kind == "Namespace"
	}

	// RetainAllButNamespaces a func to retain all but namespaces
	RetainAllButNamespaces FilterFunc = func(obj runtime.RawExtension) bool {
		gvk := obj.Object.GetObjectKind().GroupVersionKind()
		return gvk.Kind != "Namespace"
	}
)

// FilterFunc a function to retain an object or not
type FilterFunc func(runtime.RawExtension) bool

// Filter filters the given objs to return only those matching the given filters (if any)
func Filter(objs []runtime.RawExtension, filters ...FilterFunc) []runtime.RawExtension {
	result := make([]runtime.RawExtension, 0, len(objs))
loop:
	for _, obj := range objs {
		for _, filter := range filters {
			if !filter(obj) {
				continue loop
			}

		}
		result = append(result, obj)
	}
	return result
}
