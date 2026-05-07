# Getting started (developer)

This is the **developer** workflow — building the installer from source. Once
v0.1 ships, end users will just download `cluster-installer.exe` and run it.

## Prerequisites

| Tool      | Version | Why |
|-----------|---------|-----|
| Go        | ≥ 1.23  | Wails backend, embed bundling |
| Node.js   | ≥ 20    | Svelte/Vite frontend |
| Wails CLI | v2.9+   | dev server, build |
| git       | any     | go-git falls back to native git for protocols it can't handle |

Install Wails CLI:

```powershell
go install github.com/wailsapp/wails@latest
```

## Vendor binaries (one-time per content release)

The exe expects `terraform.exe` and `uv.exe` to live under `app/internal/embedded/bin/`
at build time. CI populates these from pinned upstream releases. For local
development:

```powershell
# Pinned versions — update when you bump the content tag
$tfVer = "1.9.6"
$uvVer = "0.4.18"

New-Item -ItemType Directory -Force app/internal/embedded/bin | Out-Null
# Terraform
Invoke-WebRequest "https://releases.hashicorp.com/terraform/$tfVer/terraform_${tfVer}_windows_amd64.zip" -OutFile tf.zip
Expand-Archive tf.zip -DestinationPath app/internal/embedded/bin -Force
Remove-Item tf.zip
# uv
Invoke-WebRequest "https://github.com/astral-sh/uv/releases/download/$uvVer/uv-x86_64-pc-windows-msvc.zip" -OutFile uv.zip
Expand-Archive uv.zip -DestinationPath app/internal/embedded/bin -Force
Remove-Item uv.zip
```

## Run in dev

```powershell
cd app
go mod tidy
wails dev
```

Vite serves the frontend on `localhost:34115`; Wails wires the Svelte
components to the Go bindings under `frontend/wailsjs/go/main/App.*` (regenerated
on every save).

## Build the single exe

```powershell
cd app
wails build -clean -platform windows/amd64
# → app/build/bin/cluster-installer.exe
```

The output is a 30–80 MB exe (mostly Webview runtime + embedded binaries).

## Folder layout the running exe creates

```
%LOCALAPPDATA%\cluster-installer\
├── bin\          # extracted terraform.exe, uv.exe
├── runtime\      # uv-managed Python venv with ansible-core
├── content\<ref>\  # cloned content repo at the chosen tag
├── runs\<id>\    # per-run state, TF state, kubeconfig, logs
└── cache\
    └── providers\  # TF_PLUGIN_CACHE_DIR
```

## Common dev tasks

- **Regenerate Wails bindings**: just run `wails dev` once after editing
  `app/app.go`; the file appears under `frontend/wailsjs/go/main/App.ts`.
- **Edit content templates without rebuilding the exe**: bump `content/VERSION`
  + tag a new `vX.Y.Z`; in the wizard step 1, type the new tag.
- **Test ISO seed generation in isolation**:
  ```powershell
  go test ./internal/seed/...
  ```

## Known TODOs (Phase 0 → 1)

- `App.PlanRun` and `App.ApplyRun` in `app/app.go` are stubs.
- The wizard inventory form (`Step4Inventory.svelte`) currently shows raw YAML;
  swap for a structured form bound to `wizardStore.inventory`.
- Add `app/internal/runtime/uv_download.go` to fetch `uv.exe` on first launch
  if the embed bundle is missing (dev-mode convenience).
- Wire `kubernetes.core.helm` Ansible callback so Helm install progress streams
  back through `run:line` events.
