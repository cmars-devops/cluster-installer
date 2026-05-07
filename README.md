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
└── content/    # Terraform, Ansible, Helm, AutoYaST/Combustion templates
                # (will be split into its own GitHub repo for v0.1)
```

## Status

Phase 0 — skeleton. Not runnable yet.

## Build prerequisites (developer machine)

- Go ≥ 1.23
- Wails CLI v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Node.js ≥ 20

Build: `cd app && wails build` produces `app/build/bin/cluster-installer.exe`.

See [docs/getting-started.md](docs/getting-started.md) for the full developer
workflow.

## License

TBD.
