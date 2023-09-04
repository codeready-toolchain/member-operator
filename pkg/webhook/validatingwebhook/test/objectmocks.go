package test

var IncorrectRequestObjectJSON = []byte(`{
	"kind": "AdmissionReview",
	"apiVersion": "admission.k8s.io/v1",
	"request": {
		"uid": "a68769e5-d817-4617-bec5-90efa2bad6f8",
		"name": "busybox1",
		"namespace": "johnsmith-dev",
		"object": {
			"kind": "asbasbf",
			"apiVersion": "v1",
			"metadata": {
				"name": "busybox1",
				"namespace": "johnsmith-dev"
			}
		}
	}
}`)
