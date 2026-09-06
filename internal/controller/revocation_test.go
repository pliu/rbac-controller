// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"testing"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Start from a successful sync, then revoke access while another operation
// fails. The old grant must disappear and an unchanged grant must survive.
func TestManagedNamespaceRevokesBeforeSyncFailures(t *testing.T) {
	for _, failure := range []string{"namespace", "quota", "binding", "role-lookup"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			original := api.AccessMapping{Users: []string{"alice", "bob"}, ClusterRoles: []string{"view"}}
			retained := api.AccessMapping{Group: "keepers", ClusterRoles: []string{"view"}}
			mns := managedNamespaceWith("team-a", original, retained)
			mns.Generation = 1
			armed := false
			rejected := apierrors.NewInvalid(schema.GroupKind{Kind: "ResourceQuota"}, "compute", field.ErrorList{
				field.Invalid(field.NewPath("spec", "hard", "pods"), "-1", "must be non-negative"),
			})
			cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(mns,
				&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "view"}}).
				WithStatusSubresource(mns).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if armed {
						switch obj.(type) {
						case *corev1.ResourceQuota:
							if failure == "quota" {
								return rejected
							}
						case *rbacv1.RoleBinding:
							if failure == "binding" {
								return apierrors.NewForbidden(schema.GroupResource{Group: rbacv1.GroupName, Resource: "rolebindings"}, obj.GetName(), errors.New("admission denied"))
							}
						}
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if _, ok := obj.(*corev1.Namespace); ok && armed && failure == "namespace" {
						return apierrors.NewInvalid(schema.GroupKind{Kind: "Namespace"}, obj.GetName(), field.ErrorList{
							field.Invalid(field.NewPath("metadata", "labels"), "bad label", "invalid label key"),
						})
					}
					return c.Update(ctx, obj, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*rbacv1.ClusterRole); ok && armed && failure == "role-lookup" {
						return apierrors.NewInternalError(errors.New("role lookup failed"))
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
			r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}
			q := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}
			if _, err := r.Reconcile(ctx, q); err != nil {
				t.Fatal(err)
			}
			oldKey := client.ObjectKey{Namespace: mns.Name, Name: core.BindingName(mns.Name, subjectKey(original), "view")}
			keptKey := client.ObjectKey{Namespace: mns.Name, Name: core.BindingName(mns.Name, subjectKey(retained), "view")}
			var kept rbacv1.RoleBinding
			if err := cl.Get(ctx, keptKey, &kept); err != nil {
				t.Fatal(err)
			}
			if err := cl.Get(ctx, q.NamespacedName, mns); err != nil {
				t.Fatal(err)
			}
			// Remove Alice, retaining Bob and the separate keepers grant.
			mns.Spec.AccessMappings[0].Users = []string{"bob"}
			mns.Generation++
			if failure == "namespace" {
				mns.Spec.Labels = map[string]string{"bad label": "value"}
			}
			if failure == "quota" {
				mns.Spec.ResourceQuotas = []api.ResourceQuota{{Name: "compute", ResourceQuotaSpec: corev1.ResourceQuotaSpec{
					Hard: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("-1")},
				}}}
			}
			if err := cl.Update(ctx, mns); err != nil {
				t.Fatal(err)
			}
			armed = true
			if _, err := r.Reconcile(ctx, q); err == nil {
				t.Fatal("expected the injected sync failure")
			}
			if err := cl.Get(ctx, oldKey, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
				t.Fatalf("revoked grant survived sync failure: %v", err)
			}
			var gotKept rbacv1.RoleBinding
			if err := cl.Get(ctx, keptKey, &gotKept); err != nil {
				t.Fatalf("still-requested grant was removed: %v", err)
			}
			if gotKept.ResourceVersion != kept.ResourceVersion {
				t.Error("unchanged grant was rewritten")
			}
			if err := cl.Get(ctx, q.NamespacedName, mns); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(mns.Status.Conditions, "Ready")
			if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "SyncFailed" || ready.ObservedGeneration != mns.Generation {
				t.Fatalf("sync failure not reported for current spec: %+v", ready)
			}
		})
	}
}

func TestManagedNamespaceRetriesFailedRevocationBeforeWrites(t *testing.T) {
	ctx := context.Background()
	mns := managedNamespaceWith("team-a")
	// There are no mappings left in the spec, but a binding from the old spec
	// remains. A transient failure deleting it must stay on the retry queue.
	old := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name: "old-grant", Namespace: mns.Name, Labels: ownerLabels(mns.Name),
	}}
	failDelete := true
	var creates int
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(mns, old).
		WithStatusSubresource(mns).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*rbacv1.RoleBinding); ok && failDelete {
				return apierrors.NewInternalError(errors.New("deletion temporarily unavailable"))
			}
			return c.Delete(ctx, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			creates++
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}
	q := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}
	if _, err := r.Reconcile(ctx, q); err == nil || errors.Is(err, reconcile.TerminalError(nil)) {
		t.Fatalf("failed revocation must return a retryable error: %v", err)
	}
	if creates != 0 {
		t.Fatalf("made %d creates before revocation succeeded", creates)
	}
	failDelete = false
	if _, err := r.Reconcile(ctx, q); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(old), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("retry did not revoke old grant: %v", err)
	}
	if err := cl.Get(ctx, client.ObjectKey{Name: mns.Name}, &corev1.Namespace{}); err != nil {
		t.Fatalf("retry did not resume namespace reconciliation: %v", err)
	}
}
