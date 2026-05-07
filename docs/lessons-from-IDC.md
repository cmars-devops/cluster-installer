# Lessons from `P:\K3s@IDC` (operator's existing IDC deployment)

This document captures findings from the operator's already-running cluster
(11-node Ceph + 7-node K3s on ESXi) that **must** flow into this installer's
design. The reference repo is `P:\K3s@IDC` and the troubleshooting log is
its `docs/01.OpenSUSE16+Ceph-Setup/04-troubleshooting-lessons.md` (14 issues
in chronological order).

The TL;DR: same stack, same problems, already solved once. Don't re-pay them.

## Architectural impacts (not just nice-to-knows)

### 1. Agama profile delivery is HTTP, not OEMDRV

`inst.auto=device://LABEL=OEMDRV/profile.json` does **not** work with Agama
(openSUSE Leap 16+). The legacy YaST/dracut OEMDRV pickup is gone. Only
`inst.auto=http://...` (or `file:///` from local media) is supported.

Implication for this installer: the original "build a tiny OEMDRV ISO and
attach it as a second CD-ROM" approach does **not** apply to Leap/Tumbleweed
under Agama. The viable paths:

- **Embedded HTTP server in the Windows exe** that serves Agama profiles +
  squashfs + kernel/initrd, plus a kernel cmdline `inst.auto=http://<host>:<port>/profiles/<host>.json`
  — same pattern the IDC repo uses (their `serve-profiles.py` on
  `10.10.1.99:8080`, dnsmasq Proxy DHCP on `10.10.1.11`).
- **Combustion+Ignition for MicroOS** still uses the labeled-ISO pickup
  (`label=ignition`, `/combustion/script`). This path stays as-is.

We therefore **promote the embedded HTTP server from "optional" to mandatory**
for any flow that includes Agama, and we update `seed/iso.go` to reflect that
the OEMDRV variant is a no-op for Agama.

### 2. Agama profile schema specifics

Confirmed against the operator's working profiles (`P:\K3s@IDC\profiles\*.json`):

- Top-level `product.id` is `"openSUSE_Leap"` for Leap (not `"Leap"`), and
  `"Leap-Micro"` for Leap Micro 6.2.
- `network.connections[].match` accepts `{kernel, interface, driver, path}`
  only — `match.mac` is rejected by the schema validator.
- For multi-NIC nodes, differentiate by interface name (ESXi vmxnet3:
  `ens192` first, `ens224` second). Don't try to match by MAC at install
  time.
- `storage.boot.configure: true` + `device: <alias>` is required for grub
  install on the right disk.
- Post-install scripts: prefer `chroot: true` for filesystem mods, then a
  separate `chroot: false` poweroff script that triggers the `sanboot`
  (PXE) or boot-from-disk (ISO) handoff.
- Password fields use `hashedPassword: true` + SHA-512 crypt.

The operator's [profiles/ceph-core-01.json](file:///P:/K3s@IDC/profiles/ceph-core-01.json)
is a complete reference.

### 3. NetworkManager binding via post-install — `mac-address=` direct

If the install-time `network.connections[].match.driver=["vmxnet3"]` is
left as the only binding, NetworkManager re-binds `public` to whichever NIC
came up first when a second NIC is later added (`ens224` for Ceph cluster
network). Result: SSH disconnect, IP migrates to the wrong NIC, recovery is
manual.

Mitigation: in the chroot post-install script, **delete** all auto-generated
connections and **write an explicit nmconnection file with a `mac-address=`
line** for the primary NIC. New NICs added later then never collide.

```
[connection]
id=public
type=ethernet
[ethernet]
mac-address={MAC of nic0}
[ipv4]
method=manual
address1={ip}/24,{gw}
dns={dns};
[ipv6]
method=disabled
```

Implication for our installer: the wizard must collect or auto-discover the
primary NIC MAC of each VM **before** writing the Agama profile. For libvirt
this is `virsh domiflist <vm>`; for Proxmox the API returns it after VM
creation. The IDC repo's `esxi-poweroncycle-macs.py` is the ESXi analog —
PowerOn → wait for MAC table → PowerOff → record MAC → use in profile.

### 4. PXE LiveOS kernel cmdline (bare-metal v2)

When (eventually) we boot Leap 16 over PXE without an ISO mount, the
installer initrd needs to know where the squashfs lives:

```
initrd=initrd
root=live:http://<host>:<port>/repo/LiveOS/squashfs.img
rd.live.image rd.live.dir=LiveOS
inst.auto=http://<host>:<port>/profiles/<hostname>.json
inst.install_url=http://<host>:<port>/repo
```

Without `root=live:URL`, dracut spends 90s looking for a local
`/dev/disk/by-label/Install-Leap-16.0-x86_64` and drops to emergency shell.

We document this in `content/pxe/README.md` (when v2 lands) but reflect the
constraint in plan.

## Operational impacts (must show up in playbooks)

### 5. cephadm gotchas (Tentacle 20.2.x)

- `cephadm --image <img> bootstrap` — the `--image` flag must be **before**
  `bootstrap`. Putting it after gives "unrecognized arguments".
- `radosgw-admin realm create` blocks **forever** if no OSDs are `up+in`
  (it waits for a RADOS write). Order must be: spec apply → wait for OSDs
  up+in → realm/zonegroup/zone create.
- `ceph orch ps --service-type` is removed in Tentacle. Use
  `--daemon_type <type>`.
- `cephadm shell -- radosgw-admin <cmd>` is the fallback when
  `radosgw-admin` isn't installed on the host (cephadm bootstrap doesn't
  install ceph-common automatically on every node).

Our `10-ceph-cephadm.yml` playbook is rewritten to follow this order and
to use `cephadm --image` correctly.

### 6. Reusable Ceph spec file

The operator's [`config/ceph-spec.yaml`](file:///P:/K3s@IDC/config/ceph-spec.yaml)
is a clean single-file declaration of host placement + MON/MGR/MDS/OSD/RGW
service specs. We adopt the same shape as `content/manifests/ceph/spec.yaml.tmpl`
(rendered per-cluster from inventory).

Highlights worth keeping:
- BlueStore with separate `data_devices.paths=[/dev/sdb]` +
  `db_devices.paths=[/dev/sdc]` (WAL/DB on faster device).
- RGW `service_id: <realm>.<zone>` naming convention.
- `crash` and `node-exporter` as `host_pattern: "*"` (every host).

### 7. K3s install command — full args set

The operator's `install-k3s.sh` uses a substantially richer install command
than my placeholder. The args we should default to in our `20-k3s.yml`:

```
INSTALL_K3S_EXEC="server \
  --cluster-init \
  --disable=traefik --disable=servicelb \
  --tls-san=$VIP --tls-san=$NODE_IP --tls-san=$HOSTNAME \
  --node-ip=$NODE_IP \
  --advertise-address=$NODE_IP \
  --write-kubeconfig-mode=644 \
  --kubelet-arg=eviction-hard=memory.available<500Mi \
  --kubelet-arg=eviction-soft=memory.available<1Gi \
  --kubelet-arg=eviction-soft-grace-period=memory.available=30s"
INSTALL_K3S_SELINUX_WARN=false
```

`--disable=servicelb` is needed because we install MetalLB. `--disable=traefik`
because we want an upgrade-safe ingress lifecycle (chart-managed, not
embedded).

### 8. kube-vip via podman manifest, not Helm chart

The operator's working pattern generates kube-vip's DaemonSet manifest by
running the kube-vip container itself (one shot) and piping the output to
`kubectl apply`:

```
podman run --network host --rm \
  ghcr.io/kube-vip/kube-vip:$KVVERSION \
  manifest daemonset \
    --interface ens192 \
    --address $VIP \
    --inCluster --taint --controlplane --arp --leaderElection \
| kubectl apply -f -
```

This is more reliable than the Helm chart for a control-plane VIP because it
avoids the chicken-and-egg problem (the Helm chart wants a working API
server, but the API server *needs the VIP* to be HA). Sequence:
- bootstrap server-01 with `--tls-san=$VIP`
- apply RBAC (`https://kube-vip.io/manifests/rbac.yaml`)
- apply DaemonSet (generated as above)
- wait for VIP to bind on `ens192`
- only then join server-02/03 via `K3S_URL=https://$VIP:6443`

Our `20-k3s.yml` and `20-rke2.yml` are updated to follow this.

### 9. Leap Micro 6.2 specifics

- Root FS is **read-only btrfs**. Package installs go through
  `transactional-update --non-interactive pkg install <pkg>` and require a
  reboot before the change is visible.
- `k3s-selinux` cannot be installed inside Combustion's chroot context
  reliably (transactional-update needs a real root). Workaround: write a
  oneshot systemd unit in Combustion that runs on first boot, installs the
  package, marks it done, reboots:

```
[Unit]
ConditionPathExists=!/var/lib/k3s-selinux-installed
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/sbin/transactional-update --non-interactive pkg install k3s-selinux
ExecStartPost=/bin/touch /var/lib/k3s-selinux-installed
ExecStartPost=/bin/systemctl reboot
```

- swap (zram-generator) is **on by default** on Leap Micro and **must be
  disabled** for K3s. Both `systemctl disable --now zram-generator-swap.service`
  and `printf '[zram0]\nswap-size=0\n' > /etc/systemd/zram-generator.conf`.
- firewalld: `disable` only — **don't `mask`**. Masking on Leap Micro
  breaks K3s CoreDNS / nftables interaction.

### 10. Ceph CSI integration values that work

The operator's `manifests/ceph-csi-rbd/values.yaml` uses MON v2 endpoints
(port `:3300`), `cephFS.subvolumeGroup: csi`, and a `Secret` with
`stringData.userID: k8s` (NOT `admin` — they create a dedicated
`client.k8s` keyring with narrow scope). We mirror that.

The keyring creation commands worth keeping verbatim:

```
ceph auth get-or-create client.k8s-rbd \
  mon 'allow r' \
  osd 'profile rbd pool=rbd-pool'

ceph auth get-or-create client.k8s-cephfs \
  mon 'allow r' \
  mds 'allow rw' \
  osd 'allow rw tag cephfs *=cephfs'
```

### 11. MetalLB + Traefik (vs ingress-nginx)

The operator chose Traefik (K3s default ecosystem) and MetalLB. Our v0.1
plan defaulted to ingress-nginx. We keep ingress-nginx as the upstream
default but add Traefik values + MetalLB IPAddressPool to the content repo
as toggleable options, since for K3s users this is the more familiar shape.

The MetalLB pattern: **two pools**, one fixed (`autoAssign: false`,
`/32` for Traefik VIP), one floating (`autoAssign: true`, range for general
services). This matches `manifests/metallb/ipaddresspools.yaml`.

## Workflow / harness gotchas (developer experience)

These don't change the installer code but inform the dev workflow doc:

| # | Gotcha | What we do |
|---|--------|-----------|
| 8 | Python `open()` on Windows defaults to `cp949` for Korean locale → UTF-8 files crash | Wails Go reads files as bytes; only the dev-side Python tools (if any) need explicit `encoding='utf-8'`. Linted in `docs/getting-started.md`. |
| 9 | WSL `--cd` fails on paths containing `@` (e.g. `P:\K3s@IDC`) | This repo's path is `P:\Cluster-installer` (no `@`). Documented as a project-wide constraint. |
| 10 | SSH known_hosts collide on VM rebuild — both Windows AND WSL | Our SSH wrapper (`runner/ssh.go`) uses `InsecureIgnoreHostKey` for fresh-install hosts. Documented. |
| 11 | ESXi `PowerOnVM_Task` is async — polling immediately reads stale state | Not relevant to libvirt/Proxmox v1; flagged for v2 ESXi adapter. |
| 12 | Agama `match.mac` rejected by schema | Reflected in seed templates. |

## Assets we adopt directly

The following files from `P:\K3s@IDC\` are copied (or templated) into our
content repo with attribution:

| Source | Adopted as | Form |
|--------|-----------|------|
| `config/ceph-spec.yaml` | `content/manifests/ceph/spec.yaml.tmpl` | Go template |
| `manifests/ceph-csi-rbd/values.yaml` | `content/manifests/helm/ceph-csi/values.yaml.tmpl` | Updated to match |
| `manifests/ceph-csi-rbd/storageclass.yaml` | `content/manifests/ceph-csi/storageclass.yaml.tmpl` | New |
| `manifests/metallb/ipaddresspools.yaml` | `content/manifests/metallb/pools.yaml.tmpl` | New |
| `manifests/traefik/values.yaml` | `content/manifests/helm/traefik/values.yaml.tmpl` | New |
| `pxe/dnsmasq.conf` + `pxe/boot.ipxe` | `content/pxe/*` (v2 reference) | Studied, embedded later |

## Open questions to revisit

1. **ESXi as a v1 target?** The operator's actual environment is ESXi
   (192.168.1.210, Dell R760). We chose libvirt+Proxmox for v1; ESXi is a
   strong candidate for an early v1.x because the operator will want to use
   this very installer in their own IDC. The govmomi Go SDK makes it
   tractable. Decision deferred to next planning round.
2. **DNS pre-flight gate.** The operator's `install-ceph.sh` has a strict
   DNS validation step (zone exists, all A records resolve correctly). We
   should fold this into `00-preflight.yml` as a fail-fast gate — Ceph's
   `radosgw-admin realm create` and CSI bootstrap both depend on
   forward+reverse DNS and silently misbehave when records are missing.
3. **Bootstrap node MAC discovery for libvirt/Proxmox.** Mirroring the
   ESXi `poweroncycle-macs.py` pattern: PowerOn → DHCP lease wait → record
   MAC → PowerOff → write profile. For libvirt this is one `virsh
   domiflist` call after VM creation; for Proxmox the agent or QEMU API
   returns it. We don't need a separate boot cycle.
