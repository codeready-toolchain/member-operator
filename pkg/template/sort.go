package template

import (
	"k8s.io/apimachinery/pkg/runtime"
	"math"
)

const (
	Template                = "Template"
	Namespace               = "Namespace"
	ConfigMap               = "ConfigMap"
	LimitRange              = "LimitRange"
	Project                 = "Project"
	ProjectRequest          = "ProjectRequest"
	PersistentVolumeClaim   = "PersistentVolumeClaim"
	Service                 = "Service"
	Secret                  = "Secret"
	ServiceAccount          = "ServiceAccount"
	RoleBindingRestriction  = "RoleBindingRestriction"
	RoleBinding             = "RoleBinding"
	Role                    = "Role"
	Route                   = "Route"
	Job                     = "Job"
	List                    = "List"
	Deployment              = "Deployment"
	DeploymentConfig        = "DeploymentConfig"
	ResourceQuota           = "ResourceQuota"
	Pod                     = "Pod"
	ReplicationController   = "ReplicationController"
	DaemonSet               = "DaemonSet"
	ReplicaSet              = "ReplicaSet"
	StatefulSet             = "StatefulSet"
	HorizontalPodAutoScaler = "HorizontalPodAutoScaler"
	CronJob                 = "CronJob"
	BuildConfig             = "BuildConfig"
	Build                   = "Build"
	ImageStream             = "ImageStream"
)

var orderLookup = map[string]int{
	Namespace:               1,
	ProjectRequest:          1,
	Role:                    2,
	RoleBindingRestriction:  3,
	LimitRange:              4,
	ResourceQuota:           5,
	Secret:                  6,
	ServiceAccount:          7,
	Service:                 8,
	RoleBinding:             9,
	Pod:                     9,
	PersistentVolumeClaim:   10,
	ReplicaSet:              10,
	ConfigMap:               11,
	ReplicationController:   11,
	DeploymentConfig:        12,
	Deployment:              12,
	Route:                   13,
	Job:                     14,
	DaemonSet:               15,
	StatefulSet:             16,
	HorizontalPodAutoScaler: 17,
	CronJob:                 18,
	BuildConfig:             19,
	Build:                   20,
	ImageStream:             21,
}

// ByKind represents a list of Openshift objects sortable by Kind
type ByKind []runtime.RawExtension

func (a ByKind) Len() int      { return len(a) }
func (a ByKind) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByKind) Less(i, j int) bool {
	x, y := math.MaxUint8, math.MaxUint8
	if v, ok := orderLookup[Kind(a[i])]; ok {
		x = v
	}

	if v, ok := orderLookup[Kind(a[j])]; ok {
		y = v
	}

	return x < y
}

func Kind(obj runtime.RawExtension) string {
	if obj.Object != nil {
		return obj.Object.GetObjectKind().GroupVersionKind().Kind
	}
	return ""
}
