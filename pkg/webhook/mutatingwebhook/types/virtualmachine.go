package types

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec contains the specification of VirtualMachineInstance created
	Spec VirtualMachineSpec `json:"spec" valid:"required"`
}

// VirtualMachineSpec describes how the proper VirtualMachine
// should look like
type VirtualMachineSpec struct {

	// Template is the direct specification of VirtualMachineInstance
	Template *VirtualMachineInstanceTemplateSpec `json:"template"`
}

type VirtualMachineInstanceTemplateSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	ObjectMeta metav1.ObjectMeta `json:"metadata,omitempty"`
	// VirtualMachineInstance Spec contains the VirtualMachineInstance specification.
	Spec VirtualMachineInstanceSpec `json:"spec,omitempty" valid:"required"`
}

// VirtualMachineInstanceSpec is a description of a VirtualMachineInstance.
type VirtualMachineInstanceSpec struct {

	// Specification of the desired behavior of the VirtualMachineInstance on the host.
	Domain DomainSpec `json:"domain"`

	// List of volumes that can be mounted by disks belonging to the vmi.
	Volumes []Volume `json:"volumes,omitempty"`
}

type DomainSpec struct {
	// Resources describes the Compute Resources required by this vmi.
	Resources ResourceRequirements `json:"resources,omitempty"`
}

type ResourceRequirements struct {
	// Requests is a description of the initial vmi resources.
	// Valid resource keys are "memory" and "cpu".
	// +optional
	Requests v1.ResourceList `json:"requests,omitempty"`
	// Limits describes the maximum amount of compute resources allowed.
	// Valid resource keys are "memory" and "cpu".
	// +optional
	Limits v1.ResourceList `json:"limits,omitempty"`
}

// Volume represents a named volume in a vmi.
type Volume struct {
	// Volume's name.
	// Must be a DNS_LABEL and unique within the vmi.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names
	Name string `json:"name"`
	// VolumeSource represents the location and type of the mounted volume.
	// Defaults to Disk, if no type is specified.
	VolumeSource `json:",inline"`
}

// Represents the source of a volume to mount.
// Only one of its members may be specified.
type VolumeSource struct {
	// CloudInitNoCloud represents a cloud-init NoCloud user-data source.
	// The NoCloud data will be added as a disk to the vmi. A proper cloud-init installation is required inside the guest.
	// More info: http://cloudinit.readthedocs.io/en/latest/topics/datasources/nocloud.html
	// +optional
	CloudInitNoCloud *CloudInitNoCloudSource `json:"cloudInitNoCloud,omitempty"`
	// CloudInitConfigDrive represents a cloud-init Config Drive user-data source.
	// The Config Drive data will be added as a disk to the vmi. A proper cloud-init installation is required inside the guest.
	// More info: https://cloudinit.readthedocs.io/en/latest/topics/datasources/configdrive.html
	// +optional
	CloudInitConfigDrive *CloudInitConfigDriveSource `json:"cloudInitConfigDrive,omitempty"`
}

// Represents a cloud-init nocloud user data source.
// More info: http://cloudinit.readthedocs.io/en/latest/topics/datasources/nocloud.html
type CloudInitNoCloudSource struct {
	// UserDataSecretRef references a k8s secret that contains NoCloud userdata.
	// + optional
	UserDataSecretRef *v1.LocalObjectReference `json:"secretRef,omitempty"`
	// UserDataBase64 contains NoCloud cloud-init userdata as a base64 encoded string.
	// + optional
	UserDataBase64 string `json:"userDataBase64,omitempty"`
	// UserData contains NoCloud inline cloud-init userdata.
	// + optional
	UserData string `json:"userData,omitempty"`
	// NetworkDataSecretRef references a k8s secret that contains NoCloud networkdata.
	// + optional
	NetworkDataSecretRef *v1.LocalObjectReference `json:"networkDataSecretRef,omitempty"`
	// NetworkDataBase64 contains NoCloud cloud-init networkdata as a base64 encoded string.
	// + optional
	NetworkDataBase64 string `json:"networkDataBase64,omitempty"`
	// NetworkData contains NoCloud inline cloud-init networkdata.
	// + optional
	NetworkData string `json:"networkData,omitempty"`
}

// Represents a cloud-init config drive user data source.
// More info: https://cloudinit.readthedocs.io/en/latest/topics/datasources/configdrive.html
type CloudInitConfigDriveSource struct {
	// UserDataSecretRef references a k8s secret that contains config drive userdata.
	// + optional
	UserDataSecretRef *v1.LocalObjectReference `json:"secretRef,omitempty"`
	// UserDataBase64 contains config drive cloud-init userdata as a base64 encoded string.
	// + optional
	UserDataBase64 string `json:"userDataBase64,omitempty"`
	// UserData contains config drive inline cloud-init userdata.
	// + optional
	UserData string `json:"userData,omitempty"`
	// NetworkDataSecretRef references a k8s secret that contains config drive networkdata.
	// + optional
	NetworkDataSecretRef *v1.LocalObjectReference `json:"networkDataSecretRef,omitempty"`
	// NetworkDataBase64 contains config drive cloud-init networkdata as a base64 encoded string.
	// + optional
	NetworkDataBase64 string `json:"networkDataBase64,omitempty"`
	// NetworkData contains config drive inline cloud-init networkdata.
	// + optional
	NetworkData string `json:"networkData,omitempty"`
}
