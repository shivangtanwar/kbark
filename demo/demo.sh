#!/usr/bin/env bash
# Demo environment for the kbark autofix dashboard.
#
#   demo/demo.sh up                      create both Kind clusters and the healthy baseline
#   demo/demo.sh break <scenario> [ctx]  inject one failure (default context: kind-kbark-dev)
#   demo/demo.sh reset [ctx]             remove every scenario, restore the baseline
#   demo/demo.sh status                  show demo pods in both clusters
#   demo/demo.sh down                    delete both Kind clusters
#
# Scenarios: oom, bad-image, crash, missing-config, fix-config, slow-start,
#            cordon, evicted, selector
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIX="$HERE/fixtures"
NS=kbark-demo
CLUSTERS=(kbark-dev kbark-staging)
IMAGES=(busybox:1.36 polinux/stress:1.0.4)
DEFAULT_CTX=kind-kbark-dev

k() { kubectl --context "$CTX" "$@"; }

baseline() {
  k create namespace "$NS" --dry-run=client -o yaml | k apply -f - >/dev/null
  k apply -f "$FIX/healthy.yaml"
}

cmd_up() {
  for img in "${IMAGES[@]}"; do
    docker image inspect "$img" >/dev/null 2>&1 || docker pull "$img"
  done
  local saved
  saved="$(kubectl config current-context 2>/dev/null || true)"
  for c in "${CLUSTERS[@]}"; do
    if ! kind get clusters | grep -qx "$c"; then
      kind create cluster --name "$c"
    fi
    for img in "${IMAGES[@]}"; do
      # Preloading lets the demo run offline. It is best-effort: when it
      # fails the node pulls the image from the registry itself.
      kind load docker-image "$img" --name "$c" >/dev/null 2>&1 ||
        echo "note: could not preload $img into $c; the node will pull it"
    done
    CTX="kind-$c" baseline
  done
  [ -n "$saved" ] && kubectl config use-context "$saved" >/dev/null
  echo
  echo "Ready. Start the dashboard with:"
  echo "  kbark serve --contexts kind-kbark-dev,kind-kbark-staging"
}

cmd_break() {
  local scenario="${1:?scenario required}"
  CTX="${2:-$DEFAULT_CTX}"
  case "$scenario" in
    oom)
      k apply -f "$FIX/oom.yaml" ;;
    bad-image)
      k apply -f "$FIX/api-v1.yaml"
      k -n "$NS" rollout status deploy/api --timeout=90s
      k apply -f "$FIX/api-v2-bad-image.yaml" ;;
    crash)
      k apply -f "$FIX/worker-v1.yaml"
      k -n "$NS" rollout status deploy/worker --timeout=90s
      k apply -f "$FIX/worker-v2-crash.yaml" ;;
    missing-config)
      k apply -f "$FIX/missing-config.yaml" ;;
    fix-config)
      k apply -f "$FIX/reports-config.yaml" ;;
    slow-start)
      k apply -f "$FIX/slow-start.yaml" ;;
    cordon)
      for n in $(k get nodes -o name); do k cordon "$n"; done
      k apply -f "$FIX/batch.yaml" ;;
    evicted)
      k apply -f "$FIX/evicted-pod.yaml"
      k -n "$NS" patch pod cache-7c9d4-evicted --subresource=status --type=merge \
        -p '{"status":{"phase":"Failed","reason":"Evicted","message":"The node was low on resource: memory. Threshold quantity: 100Mi, available: 62Mi."}}' ;;
    selector)
      k apply -f "$FIX/selector-mismatch.yaml" ;;
    *)
      echo "unknown scenario: $scenario" >&2; exit 2 ;;
  esac
}

cmd_reset() {
  CTX="${1:-$DEFAULT_CTX}"
  for n in $(k get nodes -o name); do k uncordon "$n" >/dev/null; done
  k delete namespace "$NS" --ignore-not-found --wait=true
  baseline
}

cmd_status() {
  for c in "${CLUSTERS[@]}"; do
    echo "== kind-$c"
    kubectl --context "kind-$c" -n "$NS" get pods 2>&1 || true
  done
}

cmd_down() {
  for c in "${CLUSTERS[@]}"; do kind delete cluster --name "$c"; done
}

case "${1:-}" in
  up) cmd_up ;;
  break) shift; cmd_break "$@" ;;
  reset) shift; cmd_reset "$@" ;;
  status) cmd_status ;;
  down) cmd_down ;;
  *) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
