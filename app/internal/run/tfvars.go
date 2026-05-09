// tfvars rendering: convert Inventory + per-run staging paths into the
// JSON-shaped variables the libvirt and Proxmox Terraform stacks expect.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// libvirtNodeVar matches the object shape declared in
// content/terraform/stacks/libvirt/main.tf > variable "nodes".
type libvirtNodeVar struct {
	Name         string `json:"name"`
	MemoryMB     int    `json:"memory_mb"`
	VCPU         int    `json:"vcpu"`
	DiskGB       int    `json:"disk_gb"`
	ExtraDisksGB []int  `json:"extra_disks_gb"`
	SeedISOPath  string `json:"seed_iso_path"`
	MAC          string `json:"mac,omitempty"`
	Pool         string `json:"pool,omitempty"`        // libvirt storage pool override (per-node)
	DiskFormat   string `json:"disk_format,omitempty"` // "qcow2" (thin) or "raw" (thick)
	BootMode     string `json:"boot_mode"`             // "kernel" (Agama) or "iso" (Combustion)
	KernelPath   string `json:"kernel_path,omitempty"`
	InitrdPath   string `json:"initrd_path,omitempty"`
	Cmdline      string `json:"cmdline,omitempty"`
	// Per-node base qcow2 to clone the root disk from. Required for MicroOS
	// (boot_mode=iso); empty for Leap/Tumbleweed kernel-boot — Agama formats
	// a blank volume from scratch during install.
	BaseVolumeID string `json:"base_volume_id,omitempty"`
}

// proxmoxNodeVar matches content/terraform/stacks/proxmox/main.tf > variable "nodes".
type proxmoxNodeVar struct {
	Name         string `json:"name"`
	MemoryMB     int    `json:"memory_mb"`
	VCPU         int    `json:"vcpu"`
	DiskGB       int    `json:"disk_gb"`
	ExtraDisksGB []int  `json:"extra_disks_gb"`
	SeedISOID    string `json:"seed_iso_id"`
	MAC          string `json:"mac,omitempty"`
	DatastoreID  string `json:"datastore_id,omitempty"` // Proxmox storage override (per-node)
	FileFormat   string `json:"file_format,omitempty"`  // "qcow2" (thin) or "raw" (thick)
	Discard      bool   `json:"discard,omitempty"`      // SSD TRIM passthrough — set when format=qcow2
}

// esxiNodeVar matches content/terraform/stacks/esxi/main.tf > variable "nodes".
//
// New schema (multi-disk + multi-NIC):
//   disks: ordered list, [0] is the OS install disk; rest are blank
//          extras the guest sees as /dev/sd[bcd...].
//   nics:  ordered list, [0] is the primary (verify SSH dials its IP,
//          netplan default route lives here unless overridden).
//
// SeedISOPath / InstallISOPath stay scalar — at most two CD-ROMs per
// VM regardless of disk/NIC count. Datastore-relative paths consumed
// by vsphere_virtual_machine.cdrom.path.
type esxiNodeVar struct {
	Name           string        `json:"name"`
	MemoryMB       int           `json:"memory_mb"`
	VCPU           int           `json:"vcpu"`
	Disks          []esxiDiskVar `json:"disks"`
	NICs           []esxiNICVar  `json:"nics"`
	SeedISOPath    string        `json:"seed_iso_path"`
	InstallISOPath string        `json:"install_iso_path,omitempty"`
	GuestID        string        `json:"guest_id,omitempty"`
}

// esxiDiskVar matches the `disk` object schema in modules/esxi-vm.
type esxiDiskVar struct {
	SizeGB       int    `json:"size_gb"`
	Datastore    string `json:"datastore,omitempty"`     // empty → use VM default
	Provisioning string `json:"provisioning,omitempty"`  // thin / thick / thick-eager
	Label        string `json:"label,omitempty"`
}

// esxiNICVar matches the `nic` object schema in modules/esxi-vm.
type esxiNICVar struct {
	Network string `json:"network"` // ESXi port-group name
	MAC     string `json:"mac,omitempty"`
	Label   string `json:"label,omitempty"`
}

// renderTFVars writes runs/<id>/terraform/tfvars.json. Returns the path.
func (o *Orchestrator) renderTFVars() (string, error) {
	dir := filepath.Join(o.runDir(), "terraform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "tfvars.json")

	switch o.Inventory.Target.Type {
	case "libvirt":
		return out, o.writeLibvirtTFVars(out)
	case "proxmox":
		return out, o.writeProxmoxTFVars(out)
	case "esxi":
		return out, o.writeESXiTFVars(out)
	default:
		return "", fmt.Errorf("unknown target type %q", o.Inventory.Target.Type)
	}
}

func (o *Orchestrator) writeLibvirtTFVars(path string) error {
	nodes := make([]libvirtNodeVar, 0, len(o.Inventory.Nodes))
	for _, n := range o.Inventory.Nodes {
		nv := libvirtNodeVar{
			Name:         n.Hostname,
			MemoryMB:     defaultInt(n.MemoryGB*1024, 4096),
			VCPU:         defaultInt(n.CPU, 2),
			DiskGB:       defaultInt(n.DiskGB, 40),
			ExtraDisksGB: extraDisksFor(n, o.Inventory.Ceph),
			MAC:          n.PrimaryMAC,
			Pool:         n.Datastore,                  // libvirt pool override (per-node) — empty = stack default
			DiskFormat:   libvirtFormatFor(n.DiskProvisioning),
		}
		// Combustion ISO is always packed; Agama uses direct kernel boot.
		isoPath := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
		nv.SeedISOPath = isoPath

		switch n.OS {
		case "microos":
			nv.BootMode = "iso"
			// MicroOS clones from a base qcow2 (the immutable root). The
			// orchestrator uploads it to the libvirt pool ahead of TF
			// apply; the volume name follows a deterministic convention so
			// re-runs of the same content tag find the existing volume.
			nv.BaseVolumeID = libvirtBaseVolumeName(n.OS)
		case "leap", "tumbleweed":
			nv.BootMode = "kernel"
			// Kernel-boot domains: leave BaseVolumeID empty so libvirt
			// creates a blank root volume Agama formats during install.
			nv.KernelPath = filepath.Join(o.stagingDir, "repo", "vmlinuz")
			nv.InitrdPath = filepath.Join(o.stagingDir, "repo", "initrd")
			profileURL := o.baseURL + "/profiles/" + n.Hostname + ".json"
			squashURL := o.baseURL + "/repo/LiveOS/squashfs.img"
			installURL := o.baseURL + "/repo"
			// Kernel cmdline derived from P:\K3s@IDC PXE patterns + Agama docs.
			nv.Cmdline = fmt.Sprintf(
				"root=live:%s rd.live.image rd.live.dir=LiveOS "+
					"inst.install_url=%s inst.auto=%s",
				squashURL, installURL, profileURL,
			)
		case "ubuntu":
			// Ubuntu live-server boots from a remastered ISO attached as a
			// CD-ROM. The remastered ISO has the autoinstall cmdline baked
			// into grub.cfg, so libvirt just needs to attach it; no direct
			// kernel boot, no base volume (Subiquity formats the disk fresh).
			// Reuse SeedISOPath as the boot CD-ROM since Ubuntu does not need
			// a separate seed ISO (autoinstall data is served over HTTP).
			nv.BootMode = "iso"
			nv.SeedISOPath = filepath.Join(o.stagingDir, "iso", "install-"+n.Hostname+".iso")
		default:
			return fmt.Errorf("node %s: unsupported os %q", n.Hostname, n.OS)
		}
		nodes = append(nodes, nv)
	}

	doc := map[string]any{
		"libvirt_uri": o.Inventory.Target.Endpoint,
		"pool":        "default",
		"network_id":  "default", // TODO: wizard step 2 should let user pick
		// Stack-level base_volume_id is no longer used as the single
		// source of truth — per-node base_volume_id wins. Pass empty so
		// the stack's fallback path doesn't accidentally apply MicroOS's
		// base volume to a Leap node.
		"base_volume_id": "",
		"nodes":          nodes,
	}
	return writeJSON(path, doc)
}

func (o *Orchestrator) writeProxmoxTFVars(path string) error {
	nodes := make([]proxmoxNodeVar, 0, len(o.Inventory.Nodes))
	for _, n := range o.Inventory.Nodes {
		fileFormat, discard := proxmoxFormatFor(n.DiskProvisioning)
		nv := proxmoxNodeVar{
			Name:         n.Hostname,
			MemoryMB:     defaultInt(n.MemoryGB*1024, 4096),
			VCPU:         defaultInt(n.CPU, 2),
			DiskGB:       defaultInt(n.DiskGB, 40),
			ExtraDisksGB: extraDisksFor(n, o.Inventory.Ceph),
			MAC:          n.PrimaryMAC,
			DatastoreID:  n.Datastore, // Proxmox storage override (per-node)
			FileFormat:   fileFormat,
			Discard:      discard,
			// TODO: Proxmox seed ISOs need to be uploaded to the PVE storage
			// before TF runs. The orchestrator must call the Proxmox API to
			// upload staging/seeds/*.iso into the chosen iso datastore and
			// stash the resulting "iso_storage:iso/<name>.iso" id here.
			SeedISOID: "",
		}
		nodes = append(nodes, nv)
	}

	doc := map[string]any{
		"endpoint":      o.Inventory.Target.Endpoint,
		"api_token":     o.Inventory.Target.APIToken,
		"tls_insecure":  o.Inventory.Target.TLSInsecure,
		"ssh_username":  defaultStr(o.Inventory.Target.Username, "root"),
		"pve_node":      "pve",        // TODO: wizard should collect
		"base_iso_id":   "local:iso/openSUSE-Leap-16.0-NET-x86_64-Media.iso", // TODO: from images.yaml
		"datastore_id":  "local-lvm",
		"iso_datastore": "local",
		"bridge":        "vmbr0",
		"nodes":         nodes,
	}
	return writeJSON(path, doc)
}

// writeESXiTFVars renders tfvars.json for content/terraform/stacks/esxi.
//
// Unlike libvirt/Proxmox, ESXi has no in-place equivalent of the
// orchestrator's HTTP server: the vSphere CD-ROM backing must point at a
// file already on the datastore. The orchestrator's pre-TF stage uploads
// every node's seed ISO to a stable path under
// "[iso_datastore] cluster-installer/<run-id>/seed-<host>.iso"; this
// function only references that path — not the bytes.
//
// MicroOS today is fully supported (Combustion seed ISO does the work).
// Leap/Tumbleweed on ESXi requires Agama-aware ISO remaster (phase-1 §4)
// which is not yet implemented; the orchestrator gates that case
// upstream with a clear error rather than producing a tfvars.json that
// would silently boot Leap into the standard installer's manual flow.
func (o *Orchestrator) writeESXiTFVars(path string) error {
	// Resolve ISO datastore. Priority:
	//   1. explicit target.iso_datastore (Step 2 picker)
	//   2. target.datastore (legacy Step 2 single-datastore)
	//   3. ANY node's per-node datastore (Step 4) — first non-empty one
	//
	// Rationale: when the operator loads a saved inventory but didn't
	// re-pick a saved target, target.* is empty while node-level
	// datastores are still populated. Falling through to those covers
	// the common single-array lab without forcing a Step 2 round-trip.
	// Multi-array clusters that genuinely need a separate ISO datastore
	// can still set it explicitly in Step 2 — priority 1 wins.
	if o.Inventory.Target.ISODatastore == "" {
		switch {
		case o.Inventory.Target.Datastore != "":
			o.Inventory.Target.ISODatastore = o.Inventory.Target.Datastore
		default:
			for _, n := range o.Inventory.Nodes {
				if n.Datastore != "" {
					o.Inventory.Target.ISODatastore = n.Datastore
					break
				}
			}
		}
		if o.Inventory.Target.ISODatastore == "" {
			return fmt.Errorf("ISO upload datastore is required (Step 4 → \"VM 디스크 데이터스토어\" or Step 2)")
		}
	}
	dsRunRoot := esxiDatastoreRunRoot(o.Run.ID)
	devVM := o.Inventory.Cluster.IsDevVM()
	nodes := make([]esxiNodeVar, 0, len(o.Inventory.Nodes))
	for _, n := range o.Inventory.Nodes {
		// Per-node primary datastore. Cluster mode REQUIRES it (no
		// silent cluster-level fallback — that used to cause
		// space-exhaustion when every OSD landed on the smallest
		// array). dev-vm falls back to target.datastore.
		primaryDS := n.Datastore
		if primaryDS == "" && devVM {
			primaryDS = o.Inventory.Target.Datastore
		}
		if primaryDS == "" {
			return fmt.Errorf("node %q: datastore is required (Step 4 → \"VM 디스크 데이터스토어\")", n.Hostname)
		}
		// vSphere displays Name in its inventory tree. When the operator
		// set a separate display_name, use that — otherwise the OS
		// hostname doubles as the vSphere label (most common case).
		vmName := n.DisplayName
		if vmName == "" {
			vmName = n.Hostname
		}

		// Build the disk list — dev-vm UI populates n.Disks directly,
		// cluster mode falls through EffectiveDisks() which reuses
		// the legacy DiskGB / Ceph extraDisksFor helpers.
		extras := extraDisksFor(n, o.Inventory.Ceph)
		effDisks := n.EffectiveDisks(extras)
		disks := make([]esxiDiskVar, 0, len(effDisks))
		for _, d := range effDisks {
			disks = append(disks, esxiDiskVar{
				SizeGB:       d.SizeGB,
				Datastore:    defaultStr(d.Datastore, primaryDS),
				Provisioning: defaultStr(d.Provisioning, defaultStr(n.DiskProvisioning, "thin")),
				Label:        d.Label,
			})
		}

		// Build the NIC list. Same pattern: explicit n.NICs wins,
		// otherwise synthesise a single NIC from target.Network +
		// per-node primary MAC.
		effNICs := n.EffectiveNICs(o.Inventory.Target.Network, o.Inventory.Target.ClusterNetwork)
		nics := make([]esxiNICVar, 0, len(effNICs))
		for _, nic := range effNICs {
			nics = append(nics, esxiNICVar{
				Network: defaultStr(nic.Network, defaultStr(o.Inventory.Target.Network, "VM Network")),
				MAC:     nic.MAC,
				Label:   nic.Label,
			})
		}

		nv := esxiNodeVar{
			Name:     vmName,
			MemoryMB: defaultInt(n.MemoryGB*1024, 4096),
			VCPU:     defaultInt(n.CPU, 2),
			Disks:    disks,
			NICs:     nics,
			GuestID:  esxiGuestIDFor(n.OS),
		}
		// Per-node seed CD-ROM. Always present:
		//   MicroOS  → Combustion+Ignition (the install payload itself)
		//   Agama    → secondary CD with first-boot script + SSH keys
		//   Ubuntu   → cidata CD with user-data + meta-data (NoCloud)
		nv.SeedISOPath = dsRunRoot + "seed-" + n.Hostname + ".iso"

		// Install ISO:
		//   Leap/Tumbleweed → per-node remaster (URL has hostname, no choice)
		//   Ubuntu          → SHARED across all Ubuntu nodes (one upload).
		//                     Identity comes from the cidata CD above.
		switch n.OS {
		case "leap", "tumbleweed":
			nv.InstallISOPath = dsRunRoot + "install-" + n.Hostname + ".iso"
		case "ubuntu":
			nv.InstallISOPath = dsRunRoot + "install-ubuntu.iso"
		}
		nodes = append(nodes, nv)
	}

	doc := map[string]any{
		"vsphere_server":   esxiServerFromEndpoint(o.Inventory.Target.Endpoint),
		"vsphere_user":     defaultStr(o.Inventory.Target.Username, "root"),
		"vsphere_password": o.Inventory.Target.Password,
		"tls_insecure":     o.Inventory.Target.TLSInsecure,
		// iso_datastore is the only Step 2-level storage choice. VM disk
		// placement is per-node (each.value.datastore in the stack).
		"iso_datastore": o.Inventory.Target.ISODatastore,
		"network":       defaultStr(o.Inventory.Target.Network, "VM Network"),
		"nodes":         nodes,
	}
	return writeJSON(path, doc)
}

// esxiDatastoreRunRoot is the per-run directory uploaded ISOs land in,
// expressed as a datastore-relative prefix (NO leading slash, trailing
// slash included). Used by both the orchestrator's pre-TF upload and
// the tfvars renderer so they agree on the path.
func esxiDatastoreRunRoot(runID string) string {
	return "cluster-installer/" + runID + "/"
}

// esxiGuestIDFor maps the inventory OS to vSphere's guest_id enum. Using
// 'opensuse64Guest' (instead of 'otherLinux64Guest') is what enables
// vmxnet3 paravirt + ballooning hints — issue #4 in lessons-from-IDC.md.
func esxiGuestIDFor(os string) string {
	switch os {
	case "microos", "leap", "tumbleweed":
		return "opensuse64Guest"
	case "ubuntu":
		return "ubuntu64Guest"
	default:
		return "otherLinux64Guest"
	}
}

// esxiServerFromEndpoint strips the URL framing the wizard accepts in
// the human-friendly form ("https://192.168.1.210/") down to the bare
// host the vsphere provider expects ("192.168.1.210").
func esxiServerFromEndpoint(endpoint string) string {
	s := endpoint
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
			break
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i]
		}
	}
	return s
}

// ---- helpers ----------------------------------------------------------

func (o *Orchestrator) runDir() string {
	return filepath.Dir(o.stagingDir) // staging is a child of runs/<id>
}

// libvirtFormatFor maps disk_provisioning to libvirt's qcow2/raw choice.
// qcow2 supports sparse allocation (thin); raw is fully preallocated (thick).
// libvirt has no equivalent of ESXi's eager-zeroed beyond raw, so 'thick-eager'
// degrades to raw with a documented note.
func libvirtFormatFor(p string) string {
	switch p {
	case "thick", "thick-eager":
		return "raw"
	default:
		return "qcow2" // thin / unset
	}
}

// proxmoxFormatFor maps disk_provisioning to Proxmox file_format + discard.
// qcow2 = thin (with discard for SSD TRIM); raw = thick (no discard).
// thick-eager again degrades to raw on Proxmox.
func proxmoxFormatFor(p string) (format string, discard bool) {
	switch p {
	case "thick", "thick-eager":
		return "raw", false
	default:
		return "qcow2", true // thin: enable TRIM passthrough
	}
}

// extraDisksFor returns per-node additional virtual disk sizes (GB) the
// Terraform stack should provision. COUNT comes from the OSD device path
// lists on the node; SIZE comes from per-node fields (osd_data_size_gb /
// osd_db_size_gb / osd_wal_size_gb). The cluster-level CephSpec.OSD*GB
// fields are accepted only as a legacy fallback for inventories saved
// before per-node sizing landed. Order: data disks first, then DB, then
// WAL.
//
// Example: a node with data_devices=[/dev/sdb,/dev/sdc] osd_data_size_gb=64,
// db_devices=[/dev/sdd] osd_db_size_gb=16:
//   → 3 extra disks: [64, 64, 16]
//
// Backward compat: if data/db/wal lists are empty but legacy
// storage_devices is set, treat the first entry as data and the rest
// as DB devices.
func extraDisksFor(n inventory.NodeSpec, c inventory.CephSpec) []int {
	if !n.HasRole("ceph-osd") {
		return []int{}
	}

	dataPaths := n.DataDevices
	dbPaths := n.DBDevices
	walPaths := n.WALDevices

	if len(dataPaths) == 0 && len(n.StorageDevices) > 0 {
		dataPaths = n.StorageDevices[:1]
		if len(n.StorageDevices) > 1 {
			dbPaths = n.StorageDevices[1:]
		}
	}

	pick := func(perNode, legacy, fallback int) int {
		switch {
		case perNode > 0:
			return perNode
		case legacy > 0:
			return legacy
		default:
			return fallback
		}
	}
	dataSize := pick(n.OSDDataSizeGB, c.OSDDataDiskSizeGB, 64)
	dbSize := pick(n.OSDDBSizeGB, c.OSDDBDiskSizeGB, 16)
	walSize := pick(n.OSDWALSizeGB, c.OSDWALDiskSizeGB, 0)

	out := make([]int, 0, len(dataPaths)+len(dbPaths)+len(walPaths))
	for range dataPaths {
		out = append(out, dataSize)
	}
	for range dbPaths {
		out = append(out, dbSize)
	}
	if walSize > 0 {
		for range walPaths {
			out = append(out, walSize)
		}
	}
	return out
}

// libvirtBaseVolumeName returns the per-OS volume name we expect to find
// (or upload) in the libvirt pool. The convention is a constant so the
// orchestrator's pre-TF "ensure base volume" step and the per-node
// tfvars renderer agree on what to look for. MicroOS is the only OS
// today that needs a base volume — Leap/Tumbleweed boot directly from
// vmlinuz/initrd and format a blank volume during install.
func libvirtBaseVolumeName(os string) string {
	switch os {
	case "microos":
		return "cluster-installer-microos.qcow2"
	default:
		return ""
	}
}

func defaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}


func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
