# Cluster Installer

A Windows-native, single-executable wizard that builds openSUSE / openSUSE MicroOS
clusters with Ceph storage and Kubernetes (RKE2 default, K3s optional) on top.
The exe ships Terraform and a self-bootstrapping Python+Ansible runtime,
and pulls all IaC content from a separately-versioned GitHub content repo.

See the development plan at
`C:\Users\CMARS\.claude\plans\terraform-ansible-rippling-pearl.md`
for the full design.

## Repo layout

```
.
├── app/        # Wails (Go + Svelte) GUI installer that compiles to one .exe
└── content/    # git submodule → cmars-devops/cluster-installer-content
                # Terraform, Ansible, Helm, Agama/Combustion templates.
                # Versioned independently of the .exe; the wizard fetches
                # this repo at runtime by git tag (Step 1 "Fetch content").
```

## Cloning

This repo uses a git submodule for `content/`. Always clone with
`--recurse-submodules`, or run `git submodule update --init` after a
plain clone:

```bash
git clone --recurse-submodules https://github.com/cmars-devops/cluster-installer.git
# or, if you already cloned:
git submodule update --init
```

One-time config so `git pull` and `git checkout` automatically pick up
submodule pointer changes:

```bash
git config --global submodule.recurse true
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full content/app
co-development workflow.

## Status

Phase 1 — runnable end-to-end on libvirt + ESXi for MicroOS and
Leap/Tumbleweed (Agama). Proxmox is wired but Agama-on-Proxmox
needs ISO remaster (tracked in `docs/phase-1-open-items.md`).

## Build prerequisites (developer machine)

- Go ≥ 1.23
- Wails CLI v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Node.js ≥ 20

Build: `cd app && go mod tidy && wails build` produces
`app/build/bin/cluster-installer.exe`.

See [docs/getting-started.md](docs/getting-started.md) for the full
developer workflow.

## License

TBD.
