#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

cluster=k8s-controller-test
image=ghcr.io/pliu/k8s-controller:latest

cleanup() {
  kind delete cluster --name "$cluster"
}
trap cleanup EXIT

kind create cluster --name "$cluster" --wait 60s
docker build -t "$image" .
kind load docker-image "$image" --name "$cluster"
kubectl apply -k config
kubectl -n k8s-controller rollout status deployment/k8s-controller --timeout=120s

# Exercise CRD admission against a real API server: the longest supported name
# must pass and the next character must fail with the name-length rule.
name63=$(printf 'n%.0s' {1..63})
for cr_kind in ManagedNamespace ClusterAccessMapping; do
  cr_spec='{}'
  if [ "$cr_kind" = ClusterAccessMapping ]; then
    cr_spec='{"group":"devs","clusterRoles":["view"]}'
  fi
  printf '{"apiVersion":"k8s.pliu.dev/v1alpha1","kind":"%s","metadata":{"name":"%s"},"spec":%s}\n' \
    "$cr_kind" "$name63" "$cr_spec" | kubectl create --dry-run=server -f - >/dev/null
  if rejection=$(printf '{"apiVersion":"k8s.pliu.dev/v1alpha1","kind":"%s","metadata":{"name":"%sx"},"spec":%s}\n' \
    "$cr_kind" "$name63" "$cr_spec" | kubectl create --dry-run=server -f - 2>&1); then
    echo "$cr_kind accepted a 64-character name" >&2
    exit 1
  fi
  [[ "$rejection" == *"metadata.name must be at most 63 characters"* ]]
done

kubectl apply -f config/sample.yaml

# The operator creates the namespace, its ResourceQuotas, and the RoleBindings for
# each AccessMapping (group or user subjects bound directly). Nothing is created
# up front; the ManagedNamespace drives it all.
for _ in {1..30}; do
  kubectl get namespace team-a >/dev/null 2>&1 && break
  sleep 1
done
kubectl get namespace team-a
for _ in {1..30}; do
  kubectl -n team-a get rolebinding -l "k8s.pliu.dev/owner-name=team-a" -o name | grep -q rolebinding && break
  sleep 1
done
kubectl -n team-a get rolebinding -l "k8s.pliu.dev/owner-name=team-a" -o name | grep -q rolebinding
kubectl -n team-a get resourcequota -l "k8s.pliu.dev/owner-name=team-a" -o name | grep -q resourcequota

# The ClusterAccessMapping reconciles into a cluster-wide ClusterRoleBinding.
for _ in {1..30}; do
  kubectl get clusterrolebinding -l "k8s.pliu.dev/owner-name=cluster-pod-readers" -o name | grep -q clusterrolebinding && break
  sleep 1
done
kubectl get clusterrolebinding -l "k8s.pliu.dev/owner-name=cluster-pod-readers" -o name | grep -q clusterrolebinding

kubectl -n k8s-controller run api-client --image=curlimages/curl:8.12.1 \
  --restart=Never --command -- sleep 300
kubectl -n k8s-controller wait --for=condition=Ready pod/api-client --timeout=120s
api_url=http://k8s-controller
kubectl -n k8s-controller exec api-client -- curl -fsS "$api_url/livez" >/dev/null

# Both endpoints are served from a watch-fed cache rather than a live LIST, so
# assert real data comes back and not just that the route answers.
for _ in {1..30}; do
  kubectl -n k8s-controller exec api-client -- \
    curl -fsS "$api_url/api/v1/managednamespaces" | grep -q team-a && break
  sleep 1
done
kubectl -n k8s-controller exec api-client -- \
  curl -fsS "$api_url/api/v1/managednamespaces" | grep -q team-a
kubectl -n k8s-controller exec api-client -- \
  curl -fsS "$api_url/api/v1/clusteraccessmappings" | grep -q cluster-pod-readers

# Every replica serves HTTP metrics, while only the leader necessarily has a
# reconcile series. Scrape all replicas so both sides of the shared registry are
# tested without depending on which pod the Service selects.
metrics=""
for server_ip in $(kubectl -n k8s-controller get pod -l app=k8s-controller \
  -o jsonpath='{range .items[*]}{.status.podIP}{"\n"}{end}'); do
  kubectl -n k8s-controller exec api-client -- curl -fsS "http://$server_ip:8080/livez" >/dev/null
  metrics+=$(kubectl -n k8s-controller exec api-client -- curl -fsS "http://$server_ip:8080/metrics")
done
grep -q controller_runtime_reconcile_total <<<"$metrics"
grep -q k8s_controller_http_requests_total <<<"$metrics"

# A quota rejected by the API server must not keep a removed user's grant live.
kubectl auth can-i list pods --as=alice@example.com -n team-a | grep -qx yes
kubectl patch managednamespace team-a --type=merge \
  -p '{"spec":{"accessMappings":[],"resourceQuotas":[{"name":"compute","hard":{"pods":"-1"}}]}}'
kubectl wait managednamespace/team-a \
  --for='jsonpath={.status.conditions[?(@.type=="Ready")].reason}=SyncFailed' --timeout=60s
for _ in {1..30}; do
  remaining=$(kubectl -n team-a get rolebinding -l "k8s.pliu.dev/owner-name=team-a" -o name)
  [ -z "$remaining" ] && break
  sleep 1
done
[ -z "$remaining" ]
! kubectl auth can-i list pods --as=alice@example.com -n team-a

# An ordinary ManagedNamespace retains its Namespace when it is deleted.
kubectl delete managednamespace team-a --wait=true --timeout=60s
kubectl get namespace team-a >/dev/null

# Bindings only get cleaned up by the finalizer, so tearing the operator down
# before its custom resources -- then force-removing finalizers to unstick them
# -- leaves live grants behind. A successor under the same name must reclaim
# them: generated names derive from the owner's name, so the bindings it still
# wants are adopted and the ones it no longer wants are pruned. Identifying the
# owner any more narrowly than by name would leave the dropped grant in place.
bindings() {
  kubectl -n team-b get rolebinding -l "k8s.pliu.dev/owner-name=team-b" \
    -o jsonpath='{range .items[*]}{.roleRef.name}{"\n"}{end}' 2>/dev/null | sort || true
}
both=$(printf 'pod-reader\nview')

kubectl apply -f - <<'YAML'
apiVersion: k8s.pliu.dev/v1alpha1
kind: ManagedNamespace
metadata: {name: team-b}
spec:
  accessMappings:
    - group: team-b-readers
      clusterRoles: [pod-reader, view]
YAML
for _ in {1..30}; do
  [ "$(bindings)" = "$both" ] && break
  sleep 1
done
[ "$(bindings)" = "$both" ]

# Stop the operator so the finalizer cannot run, then force the resource out.
kubectl -n k8s-controller scale deployment/k8s-controller --replicas=0
for _ in {1..60}; do
  kubectl -n k8s-controller get pod -l app=k8s-controller -o name | grep -q . || break
  sleep 1
done
kubectl delete managednamespace team-b --wait=false
kubectl patch managednamespace team-b --type=merge -p '{"metadata":{"finalizers":[]}}'
for _ in {1..30}; do
  kubectl get managednamespace team-b >/dev/null 2>&1 || break
  sleep 1
done
! kubectl get managednamespace team-b >/dev/null 2>&1
# Both grants outlived their owner.
[ "$(bindings)" = "$both" ]

kubectl -n k8s-controller scale deployment/k8s-controller --replicas=3
kubectl -n k8s-controller rollout status deployment/k8s-controller --timeout=180s
# With no owner left there is nothing to reconcile, so the operator coming back
# does not sweep them: only a successor can. This is why the README says to
# delete the custom resources before removing the operator.
[ "$(bindings)" = "$both" ]

kubectl apply -f - <<'YAML'
apiVersion: k8s.pliu.dev/v1alpha1
kind: ManagedNamespace
metadata: {name: team-b}
spec:
  accessMappings:
    - group: team-b-readers
      clusterRoles: [pod-reader]
YAML
for _ in {1..60}; do
  [ "$(bindings)" = "pod-reader" ] && break
  sleep 1
done
# The kept grant was adopted and the dropped one reclaimed and pruned.
[ "$(bindings)" = "pod-reader" ]
