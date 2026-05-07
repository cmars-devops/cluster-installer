# Phase 1 — open items

The orchestrator at `app/internal/run/orchestrator.go` now runs the full
pipeline end-to-end (HTTP server → seed render → terraform → SSH wait →
ansible 00-40), but four pieces are still placeholders that must land
before a clean libvirt+Leap+RKE2 run completes successfully.

## 1. ISO extraction (Agama direct kernel boot)

For Leap/Tumbleweed under Agama the orchestrator's tfvars renderer
references `<staging>/repo/vmlinuz` + `<staging>/repo/initrd` +
`<staging>/repo/LiveOS/squashfs.img`. These files don't appear by magic;
the netinstall ISO listed in `content/images.yaml` must be downloaded once
and extracted into the run's staging directory.

Pattern from the IDC reference (`P:\K3s@IDC\scripts\remaster-iso.sh`):

```bash
xorriso -osirrox on -indev openSUSE-Leap-16.0-NET.iso -extract / repo/
cp repo/boot/x86_64/loader/{linux,initrd} repo/boot/
```

Implementation plan:
- New package `app/internal/imagecache/` that downloads + verifies (sha256)
  + caches the ISO under `%LOCALAPPDATA%\cluster-installer\cache\images\<sha>\`.
- Extraction via Go's `github.com/diskfs/go-diskfs` (already a dependency)
  reading the ISO9660 filesystem, OR shelling to a bundled `xorriso.exe`.
  Go-diskfs is preferred — keeps the no-external-tools promise.
- Hard-link or symlink the extracted dir into `runs/<id>/staging/repo/`
  so the HTTP server serves it without copying ~1.8GB per run.

Trigger: orchestrator's `startHTTPAndRenderSeeds` stage, before per-node
seed rendering, only when the run has any Leap/Tumbleweed node.

## 2. libvirt base volume management

`tfvars.go > baseVolumeIDFor` returns a hard-coded volume name like
`openSUSE-Leap-16.0-base.qcow2`. Real flow needs:

1. Look up the OS image in `content/images.yaml` (reachable via the
   `imagecache` package above).
2. Upload the qcow2 to the libvirt pool over SSH (or libvirt API) — once
   per content tag, cached on the libvirt host.
3. Return the actual `libvirt_volume.id`.

For MicroOS the qcow2 is the bootable image itself; for Leap/Tumbleweed the
qcow2 is unused (we boot the kernel directly), but the module currently
demands a base_volume_id for the root disk's backing chain. Either:
- (a) Build a minimal blank qcow2 once and reuse it, or
- (b) Refactor `libvirt-vm` to allow `base_volume_id == ""` for kernel-boot
  domains (the root volume is created blank during install).

Option (b) is cleaner and aligns with how Anaconda/Agama actually work —
the installer creates the partition table from scratch.

## 3. ESXi target backend (govmomi adapter)

The wizard captures ESXi targets — Step 2 has a three-card layout
(libvirt / Proxmox / ESXi) with ESXi-specific fields: vSphere endpoint
(`https://192.168.1.210/`), root username + password (shared by SOAP
API and SSH per ESXi convention), optional SSH private key, datastore
for VM disks + seed ISOs, optional separate ISO-upload datastore, port
group name, and `tls_insecure` defaulting to true (ESXi labs are
self-signed). The schema validates these through to `target.type=esxi`,
and `state.Run` persists them to `runs/<id>/run.json`.

What's missing for execution:

- **govmomi-based Terraform module.** The reference IDC repo uses
  raw `curl` against the ESXi SOAP API for VM lifecycle. Our v1.x
  shape: a small Go package `internal/runner/esxi/` wrapping the
  vmware/govmomi SDK to (a) upload ISOs to a datastore via the
  HTTP `/folder` endpoint, (b) create VMs via `CreateVM_Task` with
  `guestId=opensuse64Guest` (the IDC lessons document this — using
  `otherLinux64Guest` skips VMware-specific NIC optimizations), (c)
  set boot order with explicit `deviceKey=4000` for NIC + `2000` for
  disk (issue #6 in lessons-from-IDC.md), (d) `PowerOnVM_Task` then
  poll status (issue #8 — async, can't poll immediately).
- **Agama profile delivery.** Same constraint as Proxmox: ESXi VMs
  can't take a kernel cmdline directly through the SOAP API in any
  clean way. Either ship a remastered netinstall ISO with
  `inst.auto=http://HOST/profiles/by-mac/${net_default_mac}.json`
  baked into grub.cfg, or let Combustion/MicroOS handle the seed
  drive (already works for that flow).
- **MAC pre-allocation** (item 4 below) is doubly important on ESXi
  because the VM's MAC isn't known until after `CreateVM_Task` returns,
  and we need it pre-render to bind the NM connection.

## 4. Proxmox + Agama path

Proxmox's `bpg/proxmox` provider does not expose direct-kernel-boot QEMU
arguments cleanly. Two viable paths for v1.x:

**(A) ISO remaster.** Mutate `boot/grub/grub.cfg` of the netinstall ISO
   so the default menu entry includes our `inst.auto=URL` parameter. The
   IDC operator deemed per-VM remastering inefficient — but a *cluster-wide*
   remaster (one ISO with `inst.auto=http://HOST/profiles/by-mac/${net_default_mac}.json`)
   is fine. The orchestrator then writes per-MAC profile files and the
   grub line resolves correctly per VM.

**(B) Pre-built remastered images.** Ship a tiny Go ISO mutator
   (`internal/imagemaster/`) that takes the upstream ISO + a small overlay
   directory and emits a new ISO whose grub.cfg references our HTTP host.
   Per content release, not per VM.

Recommendation: **(A) with by-MAC profile lookup.** The HTTP server already
hosts `profiles/<host>.json`; symlink `profiles/by-mac/<MAC>.json →
profiles/<host>.json` after the orchestrator discovers each VM's MAC.
Grub variable substitution does the rest. No per-VM ISO build.

Out of scope for the libvirt-first MVP — track but don't block on it.

## 5. MAC discovery for NetworkManager binding

The Agama post-install script writes
`/etc/NetworkManager/system-connections/public.nmconnection` with an
explicit `mac-address={{ .Node.PrimaryMAC }}` line so that adding a second
NIC later cannot rebind the `public` connection (issue #5 in
[lessons-from-IDC.md](lessons-from-IDC.md)). For this to work the
orchestrator must know each VM's MAC *before* writing the seed.

Two acceptable strategies:

- **Pre-allocate MACs.** The orchestrator generates random locally-administered
  MACs (prefix `52:54:00:` for libvirt or `BC:24:11:` for Proxmox) before
  TF apply, writes them into the inventory, and pins them in the TF stack
  via `network_interface.mac`. Then the seed knows the MAC ahead of time.
  This is the cleaner approach and what we should land first.

- **Two-pass.** First TF apply creates VMs with a non-MAC-bound seed,
  orchestrator queries the libvirt/Proxmox API for the assigned MAC,
  rewrites the NM connection via SSH on first boot, reboots. More moving
  parts; reserve as a fallback.

`tfvars.go` is wired to pass `mac` if `NodeSpec.PrimaryMAC` is set, but
the wizard doesn't populate that field yet. Adding a "generate MACs" step
in `Step4Inventory` (single button, deterministic from cluster name +
hostname) is the smallest plumbing.

## 6. Cancellation + cleanup contract

On user-cancel mid-run (Ctrl-C or wizard "Cancel" button), the orchestrator
should:

- Stop the HTTP server (already deferred).
- Optionally `terraform destroy` half-created VMs — gated by an explicit
  user confirmation, since the half-created VMs may still hold useful
  state (logs, partial OS install).
- Mark the run `state.StageFailed` with `cancelled by user`.

Ctrl-C handling is not yet wired in `App.ApplyRun`; current `ctx` is just
the Wails app context.

---

## Done

- **Topology-aware stage gating** — `Orchestrator.pipelineStages` now
  composes the stage list from `cluster.topology` (combined / ceph-only /
  k8s-only). Skipped stages emit `run:stage-skipped` for Step 6 to dim and
  strike them in the flow strip.
- **MAC pre-allocation** (closes §5) — `Orchestrator.Apply` derives a
  deterministic locally-administered MAC per node from
  `sha256(cluster + "/" + hostname)` using the right OUI for each target
  (libvirt 52:54:00, Proxmox BC:24:11, ESXi 00:50:56 with the high byte
  masked to ≤0x3F). The resulting `PrimaryMAC` is persisted to `run.json`
  before stages start, so seed renders + tfvars see the same address.
- **Cancellation contract** (closes §6) — `App.ApplyRun` now derives a
  per-run cancellable context tracked in `App.runCancels`; new
  `App.CancelRun(runID)` triggers the cancel func, killing the running
  terraform/ansible child. `Orchestrator.fail` distinguishes
  `context.Canceled` from real errors and writes `LastError = "<stage>:
  cancelled by user"`. Step 6 has a `danger`-variant Cancel button while
  a run is mid-flight (guarded by `confirm()`). Half-created VMs are
  intentionally NOT destroyed on cancel — that's a separate user action.

---

These six items together are roughly 4–6 dev-days (ESXi adds about a
day for the govmomi adapter). Item 5 (MAC pre-allocation) is the
cheapest unblocker for a happy-path libvirt+Leap+RKE2 demo.
