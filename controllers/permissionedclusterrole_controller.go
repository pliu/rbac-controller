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

package controllers

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rbacpliugithubcomv1alpha1 "github.com/pliu/rbac-controller/api/v1alpha1"
)

// PermissionedClusterRoleReconciler reconciles a PermissionedClusterRole object
type PermissionedClusterRoleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=rbac.pliu.github.com.pliu.github.com,resources=permissionedclusterroles,verbs=create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PermissionedClusterRole object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *PermissionedClusterRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here
	permissionedClusterRole := &rbacpliugithubcomv1alpha1.PermissionedClusterRole{}
	clusterRole := &rbacv1.ClusterRole{}
	clusterRole.SetName(req.Name)
	err := r.Get(ctx, req.NamespacedName, permissionedClusterRole)

	if err != nil {
		if errors.IsNotFound(err) {

			if err2 := r.Delete(ctx, clusterRole, &client.DeleteOptions{}); err != nil {
				return ctrl.Result{Requeue: true}, err2
			}
		} else {
			return ctrl.Result{Requeue: true}, err
		}
		return ctrl.Result{}, nil
	}

	clusterRole.Rules = permissionedClusterRole.Spec.Rules
	if err = r.Create(ctx, clusterRole); err != nil {
		if errors.IsAlreadyExists(err) {
			if err2 := r.Update(ctx, clusterRole); err2 != nil {
				return ctrl.Result{Requeue: true}, err2
			}
		}
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PermissionedClusterRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rbacpliugithubcomv1alpha1.PermissionedClusterRole{}).
		Complete(r)
}
