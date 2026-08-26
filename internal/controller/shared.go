// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	api "github.com/pliu/k8s-controller/api/v1alpha1"
	"github.com/pliu/k8s-controller/internal/core"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const finalizerRetryAfter = time.Second

// Helpers shared by the ManagedNamespace and ClusterAccessMapping reconcilers,
// which both turn an AccessMapping's subject set into owned bindings.

// normalizedUsers is an AccessMapping's user list reduced to the set it
// denotes. Both the generated binding's name and its subject list are derived
// from this, so that two mappings naming the same users cannot agree on the
// object to write while disagreeing on its contents: that made each reconcile
// rewrite the binding, and since the reconcilers watch their own bindings,
// every rewrite enqueued another reconcile.
func normalizedUsers(users []string) []string {
	u := append([]string(nil), users...)
	sort.Strings(u)
	return slices.Compact(u)
}

func subjectsFor(am api.AccessMapping) []rbacv1.Subject {
	if am.Group != "" {
		return []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "Group", Name: am.Group}}
	}
	users := normalizedUsers(am.Users)
	subs := make([]rbacv1.Subject, 0, len(users))
	for _, u := range users {
		subs = append(subs, rbacv1.Subject{APIGroup: rbacv1.GroupName, Kind: "User", Name: u})
	}
	return subs
}

// subjectKey identifies an AccessMapping's subject set so its generated binding
// name is stable and distinct from sibling mappings.
func subjectKey(am api.AccessMapping) string {
	if am.Group != "" {
		return "g:" + am.Group
	}
	return "u:" + core.Hash(strings.Join(normalizedUsers(am.Users), "\x00"))
}

// ownerLabels stamp a generated object with its owning custom resource so drift
// can be detected and stale objects cleaned up without adopting anything else.
// The owner's name is the whole identity: it is what the prunes select on, and
// it survives the owner being deleted and recreated, which a UID would not.
func ownerLabels(name string) map[string]string {
	return map[string]string{core.LabelManagedBy: core.ManagedBy, core.LabelOwnerName: name}
}

// terminal marks the errors that retrying cannot fix. An Invalid response means
// the API server rejected the object this spec asks for, so the same spec will
// be rejected identically forever; controller-runtime counts it in
// controller_runtime_terminal_reconcile_errors_total and stops requeuing it.
// Editing the spec enqueues a fresh reconcile, and the cache resync still
// retries every sync-period, so nothing is stuck as a result.
func terminal(e error) error {
	if apierrors.IsInvalid(e) {
		return reconcile.TerminalError(e)
	}
	return e
}

// conditionMessage renders a sync failure for a status condition, bounded well
// under the API server's 32Ki message limit so that reporting a failure cannot
// itself fail. The cut is made valid again because it may land mid-rune.
func conditionMessage(e error) string {
	const max = 1024
	m := e.Error()
	if len(m) > max {
		m = strings.ToValidUTF8(m[:max], "") + "…"
	}
	return m
}

// ownerRequests maps a generated object carrying ownerLabels back to the
// cluster-scoped custom resource named by its owner-name label.
func ownerRequests(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetLabels()[core.LabelManagedBy] != core.ManagedBy {
		return nil
	}
	name := obj.GetLabels()[core.LabelOwnerName]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name}}}
}
