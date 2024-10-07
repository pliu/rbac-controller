/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PermissionedClusterRoleSpec defines the desired state of PermissionedClusterRole
type PermissionedClusterRoleSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Rules             []rbacv1.PolicyRule `json:"rules,omitempty"`
	AllowedPrincipals []string            `json:"allowedPrincipals,omitempty"`
}

// PermissionedClusterRoleStatus defines the observed state of PermissionedClusterRole
type PermissionedClusterRoleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// PermissionedClusterRole is the Schema for the permissionedclusterroles API
type PermissionedClusterRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PermissionedClusterRoleSpec   `json:"spec,omitempty"`
	Status PermissionedClusterRoleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PermissionedClusterRoleList contains a list of PermissionedClusterRole
type PermissionedClusterRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PermissionedClusterRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PermissionedClusterRole{}, &PermissionedClusterRoleList{})
}
