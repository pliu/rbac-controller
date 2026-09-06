// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"slices"
	"testing"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// A cached read can lag another controller's finalizer edit. Both adding and
// removing our finalizer must conflict on that stale snapshot, then succeed
// after a new read without losing or resurrecting the other finalizer.
func TestFinalizerPatchesPreserveConcurrentEdits(t *testing.T) {
	for _, kind := range []string{"ManagedNamespace", "ClusterAccessMapping"} {
		for _, phase := range []string{"add", "remove"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				ctx := context.Background()
				const keep = "example.com/keep"
				const departing = "example.com/departing"
				metadata := metav1.ObjectMeta{Name: "team-a"}
				if phase == "remove" {
					now := metav1.Now()
					metadata.DeletionTimestamp = &now
					metadata.Finalizers = []string{core.Finalizer, departing, keep}
				}
				var obj client.Object = &api.ManagedNamespace{ObjectMeta: metadata}
				if kind == "ClusterAccessMapping" {
					obj = &api.ClusterAccessMapping{ObjectMeta: metadata}
				}
				injected := false
				cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(obj).
					WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(ctx context.Context, c client.WithWatch, stale client.Object, patch client.Patch, opts ...client.PatchOption) error {
							if !injected {
								injected = true
								fresh := stale.DeepCopyObject().(client.Object)
								if err := c.Get(ctx, client.ObjectKeyFromObject(stale), fresh); err != nil {
									return err
								}
								if phase == "add" {
									fresh.SetFinalizers(append(fresh.GetFinalizers(), keep))
								} else {
									fresh.SetFinalizers(slices.DeleteFunc(fresh.GetFinalizers(), func(f string) bool { return f == departing }))
								}
								if err := c.Update(ctx, fresh); err != nil {
									return err
								}
							}
							return c.Patch(ctx, stale, patch, opts...)
						},
					}).Build()
				var reconcile func(context.Context, ctrl.Request) (ctrl.Result, error)
				if kind == "ManagedNamespace" {
					reconcile = (&ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}).Reconcile
				} else {
					reconcile = (&ClusterAccessReconciler{CachedClient: cl, LiveReader: cl}).Reconcile
				}
				q := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
				if _, err := reconcile(ctx, q); !apierrors.IsConflict(err) {
					t.Fatalf("stale finalizer patch error = %v, want Conflict for retry", err)
				}
				if _, err := reconcile(ctx, q); err != nil {
					t.Fatalf("retry with fresh state: %v", err)
				}
				if err := cl.Get(ctx, q.NamespacedName, obj); err != nil {
					t.Fatal(err)
				}
				want := []string{keep}
				if phase == "add" {
					want = append(want, core.Finalizer)
				}
				got := slices.Clone(obj.GetFinalizers())
				slices.Sort(want)
				slices.Sort(got)
				if !slices.Equal(got, want) {
					t.Errorf("finalizers = %v, want %v", got, want)
				}
			})
		}
	}
}
