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

## Embedded HTTP server (`internal/httpserve`) — lifecycle

The installer runs a small HTTP server during a wizard run — bound to
`0.0.0.0:<ephemeral>` on the host, serving from a per-run staging directory
under `%LOCALAPPDATA%\cluster-installer\runs\<run-id>\staging\`. This is
**not optional**. Agama (openSUSE Leap 16+) does not pick up
`inst.auto=device://OEMDRV/...`; it only fetches the profile from
`inst.auto=http://<host>:<port>/profiles/<name>.json`.

### Who starts it, when, on what address

The orchestrator at [`internal/run/orchestrator.go`](../app/internal/run/orchestrator.go)
starts the server as the first stage of `App.ApplyRun`:

1. `netutil.PickAdvertiseIP(target.endpoint)` resolves the Windows local IP
   that routes to the target. On a multi-homed laptop (Wi-Fi + LAN +
   Hyper-V vSwitch) this avoids the silent failure where VMs can't reach
   the chosen IP — a UDP-dial trick lets the OS pick the correct NIC
   automatically.
2. `httpserve.Server.Start(ctx)` binds `0.0.0.0:0` (ephemeral port).
3. `Server.WaitReady` blocks until the listener is up, then `Server.URL(hostIP)`
   returns the canonical base URL (e.g. `http://10.10.1.99:54321`). The
   orchestrator emits a `run:server-listening` event so the Step 6 UI can
   show it to the user.
4. Per-node Agama profiles render into `staging/profiles/<host>.json` and
   Combustion ISOs into `staging/seeds/seed-<host>.iso`. The kernel cmdline
   passed into Terraform contains `inst.auto=<baseURL>/profiles/<host>.json`.
5. On run completion (success or failure), the deferred `httpSrv.Stop()`
   shuts the listener down.

The app process itself does not run a server outside of an active run — no
inbound listener, no firewall surface, no exposure when the wizard is idle.

### Windows Firewall behavior (no admin needed)

On Windows 10/11, the first time anything outside the host tries to reach
the listener, Windows Defender Firewall pops a one-time dialog: "Do you
want to allow `cluster-installer.exe` to communicate on Private/Public
networks?" The user clicks "Allow access" once and Windows persists the
rule scoped to that exe. **No admin elevation is needed for this path.**

We deliberately do *not* try to add an `New-NetFirewallRule` programmatically
— that requires admin and breaks the "no admin install" promise. Instead the
Step 6 wizard emits a `run:firewall-hint` event with a banner reminding the
user to expect the dialog. Once the rule is granted the dialog never
re-appears for that exe path.

If the user's environment forbids the dialog (kiosked corporate laptop), the
operator can add the rule out-of-band:
```powershell
New-NetFirewallRule -DisplayName "Cluster Installer (HTTP)" `
  -Direction Inbound -Action Allow -Program "C:\Path\to\cluster-installer.exe"
```

### What's served

| Path | Contents | Served to |
|------|----------|-----------|
| `/profiles/<host>.json` | Rendered Agama profile per node | Leap/Tumbleweed installer |
| `/seeds/seed-<host>.iso` | Combustion+Ignition ISO (also attached to VM) | (mirror only) |
| `/repo/...` | Extracted ISO contents (squashfs, vmlinuz, initrd) | PXE/direct-kernel boot, Phase 1+ |
| `/boot.ipxe` | iPXE chain script per MAC | bare-metal v2 only |

### Combustion fallback

For MicroOS, the labeled-ISO path (`label=ignition`, `/combustion/script`)
still works and is preferred — the HTTP server is a backup delivery channel
there.

See [lessons-from-IDC.md](lessons-from-IDC.md) §1 for why we adopted this.
