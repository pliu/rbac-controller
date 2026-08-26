// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, rbacv1.AddToScheme, api.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

// A ManagedNamespace name is validated as a DNS subdomain, but the Namespace it
// asks for is validated as a DNS label, so a name containing a dot is accepted
// by the API server and then rejected forever. The failure has to reach the
// object's status, and retrying it has to stop.
func TestInvalidNamespaceNameIsReportedAndTerminal(t *testing.T) {
	name := "team.a"
	mns := &api.ManagedNamespace{ObjectMeta: metav1.ObjectMeta{
		Name:       name,
		UID:        types.UID("mns-uid"),
		Generation: 3,
		Finalizers: []string{core.Finalizer},
	}}
	// Status from an earlier pass that did complete; a failure must not erase it.
	mns.Status.ObservedGeneration = 2
	mns.Status.InvalidReferences = []api.InvalidReference{{ClusterRole: "gone", Reason: "ClusterRole not found"}}

	rejected := apierrors.NewInvalid(schema.GroupKind{Kind: "Namespace"}, name, field.ErrorList{
		field.Invalid(field.NewPath("metadata", "name"), name, "a lowercase RFC 1123 label must not contain '.'"),
	})
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(mns).
		WithStatusSubresource(mns).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Namespace); ok {
					return rejected
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r := &ManagedNamespaceReconciler{CachedClient: cl, LiveReader: cl}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(mns)})
	if err == nil {
		t.Fatal("the rejected namespace name did not surface as an error")
	}
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("error is not terminal, so the queue retries a spec that can never succeed: %v", err)
	}

	var got api.ManagedNamespace
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(mns), &got); err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("no Ready condition was written, so the failure is invisible on the object")
	}
	if ready.Status != metav1.ConditionFalse || ready.Reason != "SyncFailed" {
		t.Errorf("Ready = %s/%s, want False/SyncFailed", ready.Status, ready.Reason)
	}
	if !strings.Contains(ready.Message, "RFC 1123") {
		t.Errorf("condition message does not carry the cause: %q", ready.Message)
	}
	// The condition is what tells an operator the failure applies to the spec
	// they just wrote, rather than being left over from an older one.
	if ready.ObservedGeneration != 3 {
		t.Errorf("condition observedGeneration = %d, want 3", ready.ObservedGeneration)
	}
	// A pass that stopped early learned nothing, so it must not overwrite what
	// the last completed pass recorded.
	if got.Status.ObservedGeneration != 2 {
		t.Errorf("status.observedGeneration = %d, want 2 (untouched by a failed pass)", got.Status.ObservedGeneration)
	}
	if len(got.Status.InvalidReferences) != 1 {
		t.Errorf("invalidReferences = %v, want the previous pass's entry retained", got.Status.InvalidReferences)
	}
}

// Only Invalid is terminal. Anything that might succeed on a later attempt has
// to stay on the rate-limited queue, while still being reported on the object.
func TestTransientSyncFailureIsReportedButRetryable(t *testing.T) {
	cam := &api.ClusterAccessMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "devs",
			UID:        types.UID("cam-uid"),
			Generation: 1,
			Finalizers: []string{core.Finalizer},
		},
		Spec: api.AccessMapping{Group: "devs", ClusterRoles: []string{"edit"}},
	}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cam, role).
		WithStatusSubresource(cam).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*rbacv1.ClusterRoleBinding); ok {
					return apierrors.NewInternalError(errors.New("etcdserver: request timed out"))
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()
	r := &ClusterAccessReconciler{CachedClient: cl, LiveReader: cl}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cam)})
	if err == nil {
		t.Fatal("the failed binding write did not surface as an error")
	}
	if errors.Is(err, reconcile.TerminalError(nil)) {
		t.Errorf("a transient failure was marked terminal, so it will never be retried: %v", err)
	}

	var got api.ClusterAccessMapping
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(cam), &got); err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("no Ready condition was written for the failure")
	}
	if ready.Status != metav1.ConditionFalse || ready.Reason != "SyncFailed" {
		t.Errorf("Ready = %s/%s, want False/SyncFailed", ready.Status, ready.Reason)
	}
	if !strings.Contains(ready.Message, "etcdserver") {
		t.Errorf("condition message does not carry the cause: %q", ready.Message)
	}
}

// A completed pass still reports success, and clears a stale failure condition.
func TestSuccessfulSyncClearsPriorFailure(t *testing.T) {
	cam := &api.ClusterAccessMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "devs",
			UID:        types.UID("cam-uid"),
			Generation: 4,
			Finalizers: []string{core.Finalizer},
		},
		Spec: api.AccessMapping{Group: "devs", ClusterRoles: []string{"edit"}},
	}
	meta.SetStatusCondition(&cam.Status.Conditions, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "SyncFailed",
		Message: "etcdserver: request timed out", ObservedGeneration: 3,
	})
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "edit"}}
	cl := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cam, role).
		WithStatusSubresource(cam).Build()
	r := &ClusterAccessReconciler{CachedClient: cl, LiveReader: cl}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cam)}); err != nil {
		t.Fatal(err)
	}

	var got api.ClusterAccessMapping
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(cam), &got); err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != "Reconciled" {
		t.Fatalf("Ready = %+v, want True/Reconciled", ready)
	}
	if got.Status.ObservedGeneration != 4 {
		t.Errorf("status.observedGeneration = %d, want 4", got.Status.ObservedGeneration)
	}
	var list rbacv1.ClusterRoleBindingList
	if err := cl.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Errorf("got %d ClusterRoleBindings, want 1", len(list.Items))
	}
}
