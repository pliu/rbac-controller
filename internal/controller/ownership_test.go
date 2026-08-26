// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"testing"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Bindings outlive their owner whenever the finalizer does not get to run --
// a force-removed finalizer, or an operator uninstalled before its custom
// resources were deleted. A successor under the same name adopts the bindings
// it still wants, because generated names derive from the owner's name, so the
// prune has to reach the ones it no longer wants too. Any owner identity finer
// than the name fails here: a recreated owner is a different object, so the
// leftovers would be invisible to it.
func TestRecreatedManagedNamespaceClearsStaleBinding(t *testing.T) {
	name := "team-a"
	mns := &api.ManagedNamespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			UID:        types.UID("new-uid"),
			Finalizers: []string{core.Finalizer},
		},
		Spec: api.ManagedNamespaceSpec{AccessMappings: []api.AccessMapping{
			{Group: "devs", ClusterRoles: []string{"edit"}},
		}},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}

	staleLabels := map[string]string{
		core.LabelManagedBy: core.ManagedBy,
		core.LabelOwnerName: name,
	}
	// The predecessor also granted "view"; the successor does not.
	stale := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      core.BindingName(name, "g:devs", "view"),
			Namespace: name,
			Labels:    staleLabels,
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"},
	}
	// The predecessor's "edit" binding, which the successor should adopt. Its
	// own UID is the witness: surviving unchanged proves it was updated in
	// place rather than deleted and recreated, which would blink the grant off.
	kept := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      core.BindingName(name, "g:devs", "edit"),
			Namespace: name,
			UID:       types.UID("kept-binding-uid"),
			Labels:    staleLabels,
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "edit"},
	}

	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(mns, ns, role, stale, kept).WithStatusSubresource(mns).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}); err != nil {
		t.Fatal(err)
	}

	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stale), &rbacv1.RoleBinding{}); err == nil {
		t.Error("the predecessor's dropped binding survived, granting access nothing references")
	}
	var got rbacv1.RoleBinding
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(kept), &got); err != nil {
		t.Fatalf("the still-wanted binding should have been adopted, not deleted: %v", err)
	}
	if got.UID != "kept-binding-uid" {
		t.Errorf("binding UID = %q, want it adopted in place rather than recreated", got.UID)
	}
	if len(got.Subjects) != 1 || got.Subjects[0].Name != "devs" {
		t.Errorf("subjects = %v, want the group the current spec asks for", got.Subjects)
	}
	if got.Labels[core.LabelOwnerName] != name {
		t.Errorf("owner-name = %q, want %q", got.Labels[core.LabelOwnerName], name)
	}
}

// The same invariant for cluster-wide grants, where a leaked binding is worse:
// it grants across every namespace.
func TestClusterAccessInitializationAddsOnlyFinalizer(t *testing.T) {
	cam := &api.ClusterAccessMapping{ObjectMeta: metav1.ObjectMeta{
		Name:   "devs",
		Labels: map[string]string{"owner": "platform"},
	}}
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cam).Build()
	r := &ClusterAccessReconciler{CachedClient: cl, LiveReader: cl}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cam)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue {
		t.Fatal("initialization did not requeue after persisting the finalizer")
	}
	var got api.ClusterAccessMapping
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(cam), &got); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&got, core.Finalizer) {
		t.Error("finalizer was not added")
	}
	if _, ok := got.Labels[core.LabelManagedBy]; ok {
		t.Errorf("ClusterAccessMapping was given a %q label", core.LabelManagedBy)
	}
	if got.Labels["owner"] != "platform" {
		t.Errorf("existing labels were not preserved: %#v", got.Labels)
	}
}

func TestRecreatedClusterAccessMappingClearsStaleBinding(t *testing.T) {
	name := "devs"
	cam := &api.ClusterAccessMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			UID:        types.UID("new-uid"),
			Finalizers: []string{core.Finalizer},
		},
		Spec: api.AccessMapping{Group: "devs", ClusterRoles: []string{"edit"}},
	}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}
	stale := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: core.BindingName(name, "g:devs", "cluster-admin"),
			Labels: map[string]string{
				core.LabelManagedBy: core.ManagedBy,
				core.LabelOwnerName: name,
			},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
	}

	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(cam, role, stale).WithStatusSubresource(cam).Build()
	r := &ClusterAccessReconciler{CachedClient: cl, LiveReader: cl}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cam)}); err != nil {
		t.Fatal(err)
	}

	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stale), &rbacv1.ClusterRoleBinding{}); err == nil {
		t.Error("the predecessor's cluster-admin binding survived its ClusterAccessMapping")
	}
	var list rbacv1.ClusterRoleBindingList
	if err := cl.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].RoleRef.Name != "edit" {
		t.Errorf("got %d bindings %v, want only the one the current spec asks for", len(list.Items), list.Items)
	}
}

// Deleting the owner still removes everything it owns, including objects left
// by a previous instance under the same name.
func TestDeletionClearsBindingsFromAPreviousInstance(t *testing.T) {
	name := "team-a"
	now := metav1.NewTime(metav1.Now().Time)
	mns := &api.ManagedNamespace{ObjectMeta: metav1.ObjectMeta{
		Name:              name,
		UID:               types.UID("new-uid"),
		Finalizers:        []string{core.Finalizer},
		DeletionTimestamp: &now,
	}}
	stale := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      core.BindingName(name, "g:devs", "view"),
			Namespace: name,
			Labels: map[string]string{
				core.LabelManagedBy: core.ManagedBy,
				core.LabelOwnerName: name,
			},
		},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"},
	}
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(mns, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, stale).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(stale), &rbacv1.RoleBinding{}); err == nil {
		t.Error("a previous instance's binding outlived deletion of the ManagedNamespace")
	}
}

// Manager clients read through informer caches. A binding created just before
// its owner is deleted may not be in that cache yet, so finalization must use
// the live API reader and must not release the owner until deletion completes.
func TestManagedNamespaceFinalizerUsesLiveReaderAndWaitsForDeletion(t *testing.T) {
	name := "team-a"
	now := metav1.Now()
	mns := &api.ManagedNamespace{ObjectMeta: metav1.ObjectMeta{
		Name:              name,
		Finalizers:        []string{core.Finalizer},
		DeletionTimestamp: &now,
	}}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name:       core.BindingName(name, "g:devs", "cluster-admin"),
		Namespace:  name,
		Finalizers: []string{"test.k8s.pliu.dev/hold"},
		Labels: map[string]string{
			core.LabelManagedBy: core.ManagedBy,
			core.LabelOwnerName: name,
		},
	}}

	live := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(mns, binding).Build()
	cached := interceptor.NewClient(live, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if bindings, ok := list.(*rbacv1.RoleBindingList); ok {
				bindings.Items = nil // simulate an informer that has not seen the create
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
	r := &ManagedNamespaceReconciler{CachedClient: cached, LiveReader: live}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cleanup did not requeue while the binding deletion was pending")
	}
	var gotMNS api.ManagedNamespace
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(mns), &gotMNS); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&gotMNS, core.Finalizer) {
		t.Fatal("owner finalizer was released before the binding disappeared")
	}
	var gotBinding rbacv1.RoleBinding
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(binding), &gotBinding); err != nil {
		t.Fatal(err)
	}
	if gotBinding.DeletionTimestamp.IsZero() {
		t.Fatal("live API binding was missed even though the cached client hid it")
	}

	result, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cleanup did not keep requeuing while the binding remained terminating")
	}
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(mns), &gotMNS); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&gotMNS, core.Finalizer) {
		t.Fatal("owner finalizer was released while a binding finalizer kept the grant alive")
	}

	base := gotBinding.DeepCopy()
	gotBinding.Finalizers = nil
	if err := live.Patch(context.Background(), &gotBinding, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(mns), &api.ManagedNamespace{}); err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("owner still exists after live API confirmed cleanup: %v", err)
	}
}

func TestClusterAccessFinalizerUsesLiveReader(t *testing.T) {
	name := "devs"
	now := metav1.Now()
	cam := &api.ClusterAccessMapping{ObjectMeta: metav1.ObjectMeta{
		Name:              name,
		Finalizers:        []string{core.Finalizer},
		DeletionTimestamp: &now,
	}}
	binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
		Name: core.BindingName(name, "g:devs", "cluster-admin"),
		Labels: map[string]string{
			core.LabelManagedBy: core.ManagedBy,
			core.LabelOwnerName: name,
		},
	}}

	live := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cam, binding).Build()
	cached := interceptor.NewClient(live, interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if bindings, ok := list.(*rbacv1.ClusterRoleBindingList); ok {
				bindings.Items = nil
				return nil
			}
			return c.List(ctx, list, opts...)
		},
	})
	r := &ClusterAccessReconciler{CachedClient: cached, LiveReader: live}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cam)}

	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cleanup did not wait for a confirming live API read")
	}
	var got api.ClusterAccessMapping
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(cam), &got); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&got, core.Finalizer) {
		t.Fatal("owner finalizer was released in the deletion-request pass")
	}
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(binding), &rbacv1.ClusterRoleBinding{}); err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("binding visible only to the live reader was not deleted: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := live.Get(context.Background(), client.ObjectKeyFromObject(cam), &api.ClusterAccessMapping{}); err == nil || !apierrors.IsNotFound(err) {
		t.Fatalf("owner still exists after cleanup was confirmed: %v", err)
	}
}
