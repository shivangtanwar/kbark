# kbark on a single server

This directory deploys the autofix console on one Linux server, watching
two Kind clusters that run on the same Docker host, published behind
nginx with TLS. It is what runs at <https://demo.kbark.dev>.

## What gets set up

| Piece | Where | Notes |
|---|---|---|
| `kind`, `kubectl`, `kbark` | `~/kbark/bin` | No root needed |
| Clusters `kbark-dev`, `kbark-staging` | Docker | One node each, with a healthy baseline in `kbark-demo` |
| kbark's credentials | `~/kbark/kubeconfig` | A service-account token per cluster bound to `deploy/rbac.yaml`; not cluster-admin |
| Console | container `kbark`, `127.0.0.1:8771` | Read-only root filesystem, no capabilities |
| Accounts | `~/kbark/users.yaml` | bcrypt hashes; generated passwords in `~/kbark/CREDENTIALS.txt` |
| Policy | `~/kbark/policy.yaml` | Per-namespace rules for what may be proposed |
| State and audit log | `~/kbark/data/` | Survive restarts |
| AI and webhook settings | `~/kbark/kbark.env` | Optional |
| Public address | nginx site + Let's Encrypt | Needs root: `install-nginx.sh` |

## Install

From a checkout, on your own machine:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/stage/bin/kbark ./cmd/kbark
mkdir -p /tmp/stage/deploy /tmp/stage/demo
cp deploy/server/compose.yaml deploy/server/policy.yaml /tmp/stage/
cp -r deploy/server/config /tmp/stage/
cp deploy/rbac.yaml deploy/demo-rbac.yaml deploy/server/{setup.sh,install-nginx.sh,nginx.conf,demo} /tmp/stage/deploy/
cp -r demo/demo.sh demo/fixtures /tmp/stage/demo/
rsync -az /tmp/stage/ SERVER:kbark/
```

On the server:

```sh
bash ~/kbark/deploy/setup.sh               # clusters, accounts, container (no root)
sudo bash ~/kbark/deploy/install-nginx.sh  # nginx site and certificate (root)
cat ~/kbark/CREDENTIALS.txt                # the generated passwords
```

`setup.sh` is safe to re-run; it keeps existing accounts and clusters.
To ship a new build, copy the new `bin/kbark` and run
`docker compose -f ~/kbark/compose.yaml restart`.

## AI explanations

`config/kbark/config.yaml` points kbark at an OpenAI-compatible endpoint
(set `base_url` and `model` there for the endpoint you use). Put that endpoint's key in
`~/kbark/kbark.env` as `KBARK_AI_API_KEY=...` and restart the container.
Without a key kbark runs normally and the Explanation card says so.

## Running demo scenarios

```sh
~/kbark/deploy/demo break oom kind-kbark-staging
~/kbark/deploy/demo break crash kind-kbark-dev
~/kbark/deploy/demo reset kind-kbark-dev
```

## Things to know

- The console is on the public internet. Sign-in is required, the session
  cookie is `Secure`, `HttpOnly` and `SameSite=Strict`, and five wrong
  passwords lock that client out for a minute. The `demo` account is
  read-only and can be shared; the admin password should not be.
- Two Kind clusters want more inotify instances than the usual default
  of 128. `install-nginx.sh` raises the limit. If a cluster's pods crash
  with "too many open files" before that script has run, that is why.
- kbark holds no cluster-admin credentials. `demo/demo.sh` uses the
  admin kubeconfig Kind wrote to `~/.kube/config`, which only the server
  account can read.
- The **Demo lab** page of the console injects the same scenarios with a
  click (admin role only). It uses a second identity, `kbark-demo-lab`
  (`deploy/demo-rbac.yaml`, kept in `~/kbark/demo-kubeconfig`), which can
  create and delete workloads in the `kbark-demo` namespace and cordon
  nodes, and nothing else. kbark's fixing identity is unchanged and
  still cannot create anything. To turn the lab off, remove the
  `--demo-kubeconfig` line from `compose.yaml` and restart.
