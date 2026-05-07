# Architecture

This document maps each box in the development plan to its concrete files.

## Boundary between exe and content repo

The exe is a **runner**: it holds embedded binaries, the Wails GUI, the run
orchestrator, and the seed-ISO builder. It contains **no Terraform code, no
Ansible playbooks, no Helm values** — those live in the content repo and are
fetched by tag at runtime.

This keeps two reliable seams:

1. The exe ships infrequently (signed Windows release).
2. The content repo can ship daily — fix a CephFS pool default? Bump the tag.
   Users pick up the fix on next run.

## Pipeline

```
inventory.yaml ─┬─► seed/templates.go ──► per-node ISO ──► terraform/{stack}/main.tf ──► VMs boot
                │                                                                        │
                └─► ansible/inventory/hosts.yml                                          │
                              │                                                          │
                              ▼                                                          │
            00-preflight ◄─── runner/ssh.go waits ◄────────────────────────────────────┘
                              │
                              ▼
            10-ceph-cephadm ──► ceph orch
                              │
                              ▼
            20-rke2 (or 20-k3s) ──► kubeconfig
                              │
                              ▼
            30-csi-ceph ──► StorageClass[rbd, cephfs, rgw]
                              │
                              ▼
            40-addons ──► kube-vip, ingress-nginx, cert-manager, prometheus, (argocd)
```

## Single source of truth

`inventory.yaml` (validated by `content/schema/inventory.schema.json`) is the
*only* file the wizard writes. All downstream artifacts are templated from it:

- `tfvars.json` (renderer: Go `encoding/json`)
- `seed-<host>.iso` (renderer: Go `text/template` → ISO9660)
- `ansible/inventory/hosts.yml` (renderer: Go template, `hosts.yml.tmpl`)
- `manifests/helm/<chart>/values.yaml.tmpl` (renderer: Ansible's Jinja2 at apply time)

If you find yourself writing config to a second file, push the field into the
inventory schema instead.

## Why two `.tmpl` flavors?

- Files rendered by the **installer** (Go) → `text/template` → `{{ .Field }}`
  (e.g. Agama, Combustion, Ignition, hosts.yml).
- Files rendered by **Ansible** (Jinja2) → `{{ var }}` (Helm values).

Both end in `.tmpl` to keep IDEs from imposing strict syntax checks. The
processor is decided by the consumer.

## State and recovery

Every run gets a UUID under `%LOCALAPPDATA%\cluster-installer\runs\<id>\`:

- `run.json` — the canonical state (stage, history, errors)
- `inventory.yaml` — frozen copy of the input
- `tfvars.json`
- `terraform/` — TF working dir (state goes here)
- `kubeconfig` — written by 20-rke2.yml's `rke2_download_kubeconf`
- `logs/<stage>.log`

`App.ResumeRun(id)` rehydrates `run.json` and lets the wizard re-enter at the
recorded stage. Each playbook is idempotent so a re-run from the failed stage
converges.

## Why no Bash anywhere on the Windows side?

The installer is a Go binary; all process orchestration uses `os/exec` against
Windows-native binaries (`terraform.exe`, `uv.exe`, `ansible-playbook.exe` from
the uv venv). The only shell scripts are inside the Combustion stage, which
runs on the *target* Linux node, not on Windows.
