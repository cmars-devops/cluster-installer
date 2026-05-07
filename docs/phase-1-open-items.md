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

## 2. libvirt base volume management — partially done

The kernel-boot half is closed (option (b) per below): `libvirt-vm`
accepts `base_volume_id == ""` and emits a blank `libvirt_volume` for
kernel-boot domains. The stack now threads per-node `base_volume_id`
through (defaults to `""`), and tfvars renders an empty value for
Leap/Tumbleweed. Done in commit `<see git log>`.

What's still manual: **MicroOS qcow2 upload to the libvirt pool.** The
tfvars renderer expects a volume named `cluster-installer-microos.qcow2`
to exist in the chosen pool ahead of `terraform apply`. The operator
prepares this once per libvirt host:

```bash
virsh vol-create-as default cluster-installer-microos.qcow2 \
    --capacity $(stat -c%s openSUSE-MicroOS.x86_64.qcow2) --format qcow2
virsh vol-upload --pool default cluster-installer-microos.qcow2 \
    openSUSE-MicroOS.x86_64.qcow2
```

A future iteration will:
1. Reuse `imagecache.EnsureImage` to download MicroOS qcow2 to
   `%LOCALAPPDATA%\cluster-installer\cache\images\<sha>\image.iso`.
2. SCP it to the libvirt host and call `virsh vol-create-as` +
   `vol-upload` over SSH.
3. Skip if the destination volume already exists with a matching size.

Tracked but non-blocking — Agama (Leap/Tumbleweed) clusters work today
without any manual upload, which covers the most common install path.

## 3. ESXi target backend — MicroOS path done

What landed in v1.x:

- **govmomi-backed Discover** — `internal/runner/esxi/discover.go` now
  logs in via `govmomi.NewClient`, reads host info + datastores
  (capacity / free / type / accessible) + networks (with VLAN
  extracted from common naming conventions like
  `Storage-Net (VLAN100)`). Single bound 20-second login per click.
- **Datastore upload helper** — `internal/runner/esxi/upload.go`
  exposes `Client.UploadFile(ctx, datastore, dsRel, localPath, emit)`
  using `Datastore.Upload` (the SOAP-attached `/folder` PUT). Progress
  via `progress.Sinker` re-emitted as 2-second `run:line` events.
  `MakeDirectory(parents=true)` for intermediate dirs, idempotent
  against `FileAlreadyExists`.
- **ESXi terraform stack** —
  `content/terraform/stacks/esxi/main.tf` + `modules/esxi-vm/main.tf`
  using `hashicorp/vsphere`. Boot order `[cdrom, disk]` (lessons #6),
  `opensuse64Guest` default for vmxnet3 paravirt (lessons #4),
  thin/thick/thick-eager → `thin / lazy / eagerZeroedThick`.
- **Orchestrator stage `datastore_upload`** — runs only when
  `target.type=esxi`, between seed render and TF init. Skipped (with
  strikethrough in Step 6) for libvirt/proxmox. Uploads each node's
  Combustion seed ISO to
  `[iso_datastore] cluster-installer/<run-id>/seed-<host>.iso`.

**What's still gated** — Leap/Tumbleweed on ESXi. The orchestrator
returns a clear error at the upload stage if the inventory has any
Leap/Tumbleweed node, because Agama profile delivery on vSphere needs
ISO remaster (see §4 below) — direct kernel boot isn't available via
the vsphere provider. **MicroOS clusters work end-to-end.**

MAC pre-allocation (originally listed alongside ESXi) was already
closed by §5 — the deterministic-from-cluster-name MAC lands in the
inventory before `vsphere_virtual_machine` runs, so `use_static_mac`
binds correctly without a two-pass dance.

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
- **ESXi MicroOS happy path** (closes §3 for MicroOS) — see §3 above for
  the breakdown. govmomi adapter, datastore upload stage, vsphere
  terraform stack, all wired. Leap/Tumbleweed on ESXi remains gated
  pending §4 (Agama ISO remaster).
- **libvirt base volume — kernel-boot path** (partially closes §2) —
  `libvirt-vm` and the libvirt stack now both accept an empty
  `base_volume_id`, threading per-node values through. tfvars renders
  empty for Leap/Tumbleweed (kernel boot — Agama formats the disk
  fresh) and a deterministic `cluster-installer-microos.qcow2` for
  MicroOS. Operators still upload the MicroOS qcow2 manually one time;
  `imagecache.EnsureImage` could be reused for an SSH-driven upload but
  that wasn't the immediate blocker.
- **ISO extraction / image cache** (closes §1) — new `internal/imagecache`
  package: parses `images.yaml`, fetches the upstream `.sha256` to derive
  a content-addressed cache dir, downloads the ISO with periodic 2-second
  progress lines, verifies sha256 on the fly, and extracts
  `boot/x86_64/loader/{linux,initrd}` + `LiveOS/squashfs.img` into
  `runs/<id>/staging/repo/` via go-diskfs (no external xorriso). Cache
  lives at `%LOCALAPPDATA%\cluster-installer\cache\images\<sha-prefix>\`
  so concurrent content tags pointing at the same upstream share storage.
  The orchestrator gates this on having any Leap/Tumbleweed node — pure
  MicroOS clusters skip the whole stage. Idempotent at both layers
  (cache dir hit → skip download; staging mtime ≥ ISO mtime → skip
  extract).

---

These six items together are roughly 4–6 dev-days (ESXi adds about a
day for the govmomi adapter). Item 5 (MAC pre-allocation) is the
cheapest unblocker for a happy-path libvirt+Leap+RKE2 demo.
