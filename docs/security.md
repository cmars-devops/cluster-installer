# Security notes

## Threat model (v0.1)

The installer runs on a trusted operator's laptop and connects out to:

1. GitHub (pull content repo over HTTPS)
2. The libvirt host (SSH) or Proxmox API (HTTPS with API token)
3. Each newly-installed cluster node (SSH, post-OS install)

There is no inbound traffic to the laptop. The exe needs no admin rights and
modifies only `%LOCALAPPDATA%\cluster-installer\`.

## SSH keys

- The wizard never generates a private key on the user's behalf in v0.1 — the
  user supplies a path to an existing private key.
- The corresponding public key is embedded into Agama/Ignition seeds so
  every freshly-booted node trusts it.
- Host keys are accepted blindly on first contact (`InsecureIgnoreHostKey`)
  because the nodes are brand-new and have no out-of-band trust anchor. This is
  documented at `runner/ssh.go:authConfig`.

## Secrets at rest

- Proxmox API tokens are persisted only inside `runs/<id>/run.json`, which is
  per-user under `%LOCALAPPDATA%`. Do **not** commit run JSONs.
- Ceph admin keyring is fetched, base64-decoded into Helm values, and **not**
  re-persisted to disk. It lives only in the cluster's `csi-rbd-secret`.

## Code signing

- Phase 5 ships the exe with Authenticode signing. Until then, SmartScreen will
  warn — instruct early-adopter users that this is expected.

## Content repo trust

- The exe pins a default content tag at build time. Operators picking a
  different tag in the wizard accept the risk that it's unreviewed.
- Phase 5 plan: sign content tags with Sigstore `cosign` and verify the
  signature in `content/git.go` before the clone is considered usable.
