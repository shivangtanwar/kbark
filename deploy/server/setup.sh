#!/usr/bin/env bash
# Sets up the kbark demo on this server, without root:
#   - kind, k3d and kubectl into ~/kbark/bin
#   - two Kind clusters (kbark-dev, kbark-staging) and a three-node k3s
#     cluster (kbark-k3s), each with a healthy baseline
#   - a least-privilege service account per cluster, and a kubeconfig
#     for kbark that uses only those accounts
#   - sign-in accounts with generated passwords
#   - the kbark container (docker compose), listening on 127.0.0.1:8771
#
# Run from ~/kbark after the files have been copied there. Safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin"
export PATH="$BIN:$PATH"
KIND_VERSION=v0.31.0
KUBECTL_VERSION=v1.35.0
NODE_IMAGE=kindest/node:v1.35.0
CLUSTERS=(kbark-dev kbark-staging)
K3D_VERSION=v5.8.3
K3S_CLUSTER=kbark-k3s
PUBLIC_URL="${KBARK_PUBLIC_URL:-https://demo.kbark.dev}"
AI_NETWORK="${KBARK_AI_NETWORK:-litellm-net}"

umask 077
mkdir -p "$BIN" "$ROOT/data" "$ROOT/config"
cd "$ROOT"

fetch() { # url dest
  [ -x "$2" ] && return
  curl -fsSL -o "$2.tmp" "$1" && chmod 755 "$2.tmp" && mv "$2.tmp" "$2"
}
fetch "https://kind.sigs.k8s.io/dl/$KIND_VERSION/kind-linux-amd64" "$BIN/kind"
fetch "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl" "$BIN/kubectl"
fetch "https://github.com/k3d-io/k3d/releases/download/$K3D_VERSION/k3d-linux-amd64" "$BIN/k3d"
chmod 755 "$BIN/kbark"

KCFG="$ROOT/kubeconfig"
DEMO_KCFG="$ROOT/demo-kubeconfig"
: > "$KCFG.new"
: > "$DEMO_KCFG.new"

# grant <admin context> <name> <API address as the kbark container sees it> <CA file>
# Gives kbark its two identities on one cluster and records them in the
# two kubeconfigs: the autofix service account (kbark-autofix
# ClusterRole, not cluster-admin), and the separate demo-lab account
# that may create demo workloads in kbark-demo and nothing else.
grant() {
  local ctx="$1" name="$2" server="$3" ca="$4" token
  kubectl --context "$ctx" apply -f "$ROOT/deploy/rbac.yaml" >/dev/null
  kubectl --context "$ctx" apply -f "$ROOT/deploy/demo-rbac.yaml" >/dev/null
  kubectl --context "$ctx" apply -f "$ROOT/demo/fixtures/healthy.yaml" >/dev/null
  token="$(kubectl --context "$ctx" -n kbark-system create token kbark --duration=8760h)"
  kubectl --kubeconfig "$KCFG.new" config set-cluster "$name" --server "$server" --certificate-authority "$ca" --embed-certs=true >/dev/null
  kubectl --kubeconfig "$KCFG.new" config set-credentials "kbark@$name" --token "$token" >/dev/null
  kubectl --kubeconfig "$KCFG.new" config set-context "$name" --cluster "$name" --user "kbark@$name" >/dev/null
  token="$(kubectl --context "$ctx" -n kbark-system create token kbark-demo-lab --duration=8760h)"
  kubectl --kubeconfig "$DEMO_KCFG.new" config set-cluster "$name" --server "$server" --certificate-authority "$ca" --embed-certs=true >/dev/null
  kubectl --kubeconfig "$DEMO_KCFG.new" config set-credentials "demo-lab@$name" --token "$token" >/dev/null
  kubectl --kubeconfig "$DEMO_KCFG.new" config set-context "$name" --cluster "$name" --user "demo-lab@$name" >/dev/null
}

ca="$(mktemp)"
for c in "${CLUSTERS[@]}"; do
  if ! kind get clusters 2>/dev/null | grep -qx "$c"; then
    kind create cluster --name "$c" --image "$NODE_IMAGE" --wait 180s
  fi
  kind get kubeconfig --internal --name "$c" | awk '/certificate-authority-data:/{print $2}' | base64 -d > "$ca"
  grant "kind-$c" "$c" "https://$c-control-plane:6443" "$ca"
done

# A third cluster on a different distribution: k3s, three nodes, so
# that node failures and DaemonSets can be shown for real.
if ! k3d cluster list "$K3S_CLUSTER" >/dev/null 2>&1; then
  k3d cluster create "$K3S_CLUSTER" --servers 1 --agents 2 --no-lb \
    --k3s-arg "--disable=traefik@server:0" --kubeconfig-update-default=false --timeout 300s
fi
k3d kubeconfig merge "$K3S_CLUSTER" --kubeconfig-merge-default >/dev/null
kubectl config view --raw -o jsonpath="{.clusters[?(@.name=='k3d-$K3S_CLUSTER')].cluster.certificate-authority-data}" | base64 -d > "$ca"
grant "k3d-$K3S_CLUSTER" "$K3S_CLUSTER" "https://k3d-$K3S_CLUSTER-server-0:6443" "$ca"
rm -f "$ca"

kubectl --kubeconfig "$KCFG.new" config use-context "${CLUSTERS[0]}" >/dev/null
mv "$KCFG.new" "$KCFG"
kubectl --kubeconfig "$DEMO_KCFG.new" config use-context "${CLUSTERS[0]}" >/dev/null
mv "$DEMO_KCFG.new" "$DEMO_KCFG"
chmod 600 "$KCFG" "$DEMO_KCFG"

if [ ! -f users.yaml ]; then
  admin_pw="$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)"
  viewer_pw="$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)"
  { echo "users:"
    printf '%s\n' "$admin_pw"  | kbark users hash shivang --name "Shivang Tanwar" --role admin | grep -v '^users:'
    printf '%s\n' "$viewer_pw" | kbark users hash demo --name "Demo Viewer" --role viewer | grep -v '^users:'
  } > users.yaml
  cat > CREDENTIALS.txt <<CRED
kbark console: $PUBLIC_URL

  admin    username: shivang   password: $admin_pw
  viewer   username: demo      password: $viewer_pw   (read-only; safe to share)

Change a password: kbark users hash <username> --role <role>, replace the
entry in users.yaml, then: docker compose -f $ROOT/compose.yaml restart
Delete this file once the passwords are stored somewhere safe.
CRED
  echo "Accounts created. Passwords are in $ROOT/CREDENTIALS.txt"
fi

[ -f kbark.env ] || cat > kbark.env <<'ENV'
# Key for the OpenAI-compatible endpoint in config/kbark/config.yaml.
# Leave empty to run without AI explanations (detection and fixes still work).
KBARK_AI_API_KEY=
# Optional: Slack-compatible webhook for incident notifications.
KBARK_NOTIFY_WEBHOOK=
ENV

cat > .env <<ENV
KBARK_UID=$(id -u)
KBARK_GID=$(id -g)
KBARK_PUBLIC_URL=$PUBLIC_URL
KBARK_AI_NETWORK=$AI_NETWORK
ENV

docker compose up -d --force-recreate
sleep 3
docker logs kbark 2>&1 | tail -12
