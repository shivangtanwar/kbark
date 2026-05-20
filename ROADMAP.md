# Roadmap

kbark is on a tight, opinionated v1. This file lists only the **next three milestones**. Anything beyond M2 is intentionally not promised.

## M0 — Skeleton & kubeconfig (in progress)

A `kbark` binary that loads `~/.kube/config`, dials the cluster, and reports green from `kbark doctor` against minikube, kind, and a real EKS context. No TUI yet.

## M1 — Pod table view

A live-updating bubbletea table of pods in the current namespace, with arrow-key navigation and a namespace switcher. Kill a pod in another terminal, the row updates within 2 seconds. CPU under 5% idle on a 200-pod cluster.

## M2 — Resource roster

Same uniform keymap for deployments, services, statefulsets, daemonsets, jobs, cronjobs, nodes, events, configmaps, secrets, ingresses. `Enter` opens a read-only describe + YAML modal.

## What's intentionally not on this list

The `?` AI diagnosis modal, log viewer, transcripts, profiles, distribution, demo, and launch. They are tracked privately and will be promoted to this roadmap as each prior milestone closes. If you need to know whether a feature is planned, open an issue rather than guessing.
