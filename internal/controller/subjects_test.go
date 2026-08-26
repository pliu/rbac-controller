// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"reflect"
	"testing"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func subjectNames(subjects []rbacv1.Subject) []string {
	out := make([]string, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, s.Name)
	}
	return out
}

func managedNamespaceWith(name string, mappings ...api.AccessMapping) *api.ManagedNamespace {
	return &api.ManagedNamespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{core.Finalizer},
		},
		Spec: api.ManagedNamespaceSpec{AccessMappings: mappings},
	}
}

// Two mappings naming the same users in different orders hash to one binding
// name. When the subject list still followed spec order they disagreed about
// that binding's contents, so every reconcile rewrote it twice -- and because
// the reconciler watches RoleBindings, each rewrite enqueued another reconcile.
// The steady state has to be quiet: an unchanged spec must write nothing.
func TestReorderedUserMappingsReachAQuietSteadyState(t *testing.T) {
	name := "team-a"
	mns := managedNamespaceWith(name,
		api.AccessMapping{Users: []string{"alice", "bob"}, ClusterRoles: []string{"edit"}},
		api.AccessMapping{Users: []string{"bob", "alice"}, ClusterRoles: []string{"edit"}},
	)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}

	var bindingWrites int
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(mns, ns, role).WithStatusSubresource(mns).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*rbacv1.RoleBinding); ok {
					bindingWrites++
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Let the first pass settle, then measure only the steady state.
	bindingWrites = 0
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if bindingWrites != 0 {
		t.Errorf("%d RoleBinding writes reconciling an unchanged spec, want 0; each one re-enqueues the owner through the RoleBinding watch", bindingWrites)
	}

	var list rbacv1.RoleBindingList
	if err := cl.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("got %d RoleBindings, want the two equivalent mappings to converge on one", len(list.Items))
	}
	if got := subjectNames(list.Items[0].Subjects); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Errorf("subjects = %v, want the sorted user set", got)
	}
}

// The subject list denotes a set, so a user repeated in the spec is bound once.
func TestDuplicateUsersCollapseToOneSubject(t *testing.T) {
	name := "team-a"
	mns := managedNamespaceWith(name,
		api.AccessMapping{Users: []string{"bob", "alice", "bob"}, ClusterRoles: []string{"edit"}},
	)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}

	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(mns, ns, role).WithStatusSubresource(mns).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}); err != nil {
		t.Fatal(err)
	}
	var list rbacv1.RoleBindingList
	if err := cl.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("got %d RoleBindings, want 1", len(list.Items))
	}
	if got := subjectNames(list.Items[0].Subjects); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Errorf("subjects = %v, want each user bound once", got)
	}
}

// Ordering and repetition in the spec must not change which object is written,
// so that reordering a user list is not a rename that churns the binding.
func TestSubjectKeyDependsOnlyOnTheUserSet(t *testing.T) {
	base := subjectKey(api.AccessMapping{Users: []string{"alice", "bob"}})
	for _, users := range [][]string{
		{"bob", "alice"},
		{"alice", "bob", "alice"},
		{"bob", "bob", "alice"},
	} {
		if got := subjectKey(api.AccessMapping{Users: users}); got != base {
			t.Errorf("subjectKey(%v) = %q, want %q", users, got, base)
		}
	}
	if same := subjectKey(api.AccessMapping{Users: []string{"alice", "carol"}}); same == base {
		t.Error("different user sets must not share a binding name")
	}
}
