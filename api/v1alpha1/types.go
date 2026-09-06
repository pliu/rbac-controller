// SPDX-License-Identifier: Apache-2.0
// +kubebuilder:object:generate=true
// +groupName=k8s.pliu.dev
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "k8s.pliu.dev", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &ManagedNamespace{}, &ManagedNamespaceList{}, &ClusterAccessMapping{}, &ClusterAccessMappingList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

// AccessMapping grants a single group OR a list of users a set of ClusterRoles.
// It is namespace-scoped as an entry in a ManagedNamespace, and cluster-wide as
// the spec of a ClusterAccessMapping. Exactly one of group or users is set.
// +kubebuilder:validation:XValidation:rule="(has(self.group) && size(self.group) > 0) != (has(self.users) && size(self.users) > 0)",message="set exactly one of group or users"
type AccessMapping struct {
	Group string `json:"group,omitempty"`
	// Users are each bound as an individual User subject. Order and repetition
	// do not matter; the set is what is granted.
	// +kubebuilder:validation:items:MinLength=1
	Users []string `json:"users,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	ClusterRoles []string `json:"clusterRoles"`
}

// ResourceQuota is one ResourceQuota applied to the managed namespace. Name is
// the object created there; the remaining fields are a Kubernetes
// ResourceQuotaSpec (hard, scopes, and scopeSelector).
type ResourceQuota struct {
	// Name is the name of the ResourceQuota created in the namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	Name                     string `json:"name"`
	corev1.ResourceQuotaSpec `json:",inline"`
}

// ManagedNamespaceSpec configures the namespace named by metadata.name. The
// resource's own name is the namespace name, so there is exactly one
// ManagedNamespace per namespace.
type ManagedNamespaceSpec struct {
	// Labels are applied to the managed Namespace and kept in sync. Labels
	// managed by other actors are preserved; controller-owned labels take
	// precedence on conflicts.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are applied to the managed Namespace and kept in sync.
	// Annotations managed by other actors are preserved; controller-owned
	// annotations take precedence on conflicts.
	Annotations map[string]string `json:"annotations,omitempty"`
	// ResourceQuotas are applied to the namespace; removing an entry removes
	// that managed quota. Names must be unique.
	// +listType=map
	// +listMapKey=name
	ResourceQuotas []ResourceQuota `json:"resourceQuotas,omitempty"`
	AccessMappings []AccessMapping `json:"accessMappings,omitempty"`
}

type InvalidReference struct {
	ClusterRole string `json:"clusterRole,omitempty"`
	Reason      string `json:"reason"`
}

type ManagedNamespaceStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	InvalidReferences  []InvalidReference `json:"invalidReferences,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=mns
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 63",message="metadata.name must be at most 63 characters"
type ManagedNamespace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ManagedNamespaceSpec   `json:"spec"`
	Status            ManagedNamespaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ManagedNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedNamespace `json:"items"`
}

type ClusterAccessMappingStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	InvalidReferences  []InvalidReference `json:"invalidReferences,omitempty"`
}

// ClusterAccessMapping binds a single group OR a list of users to a set of
// ClusterRoles cluster-wide, via ClusterRoleBindings. Its spec is an
// AccessMapping evaluated without a namespace.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=cam
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 63",message="metadata.name must be at most 63 characters"
type ClusterAccessMapping struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AccessMapping              `json:"spec"`
	Status            ClusterAccessMappingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterAccessMappingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAccessMapping `json:"items"`
}
