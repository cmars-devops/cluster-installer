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
// SeedISOPath / BaseISOPath are datastore-relative (the orchestrator uploads
// the bytes to ISODatastore via govmomi pre-TF; the path here is what
// vSphere consumes in vsphere_virtual_machine.cdrom.path).
type esxiNodeVar struct {
	Name             string `json:"name"`
	MemoryMB         int    `json:"memory_mb"`
	VCPU             int    `json:"vcpu"`
	DiskGB           int    `json:"disk_gb"`
	ExtraDisksGB     []int  `json:"extra_disks_gb"`
	SeedISOPath      string `json:"seed_iso_path"`
	BaseISOPath      string `json:"base_iso_path,omitempty"`
	MAC              string `json:"mac,omitempty"`
	Datastore        string `json:"datastore,omitempty"`      // per-node disk datastore override
	ISODatastore     string `json:"iso_datastore,omitempty"`  // per-node ISO datastore override
	DiskProvisioning string `json:"disk_provisioning,omitempty"`
	GuestID          string `json:"guest_id,omitempty"`
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
	dsRunRoot := esxiDatastoreRunRoot(o.Run.ID)
	nodes := make([]esxiNodeVar, 0, len(o.Inventory.Nodes))
	for _, n := range o.Inventory.Nodes {
		nv := esxiNodeVar{
			Name:             n.Hostname,
			MemoryMB:         defaultInt(n.MemoryGB*1024, 4096),
			VCPU:             defaultInt(n.CPU, 2),
			DiskGB:           defaultInt(n.DiskGB, 40),
			ExtraDisksGB:     extraDisksFor(n, o.Inventory.Ceph),
			SeedISOPath:      dsRunRoot + "seed-" + n.Hostname + ".iso",
			MAC:              n.PrimaryMAC,
			Datastore:        n.Datastore, // per-node placement override
			DiskProvisioning: defaultStr(n.DiskProvisioning, "thin"),
			GuestID:          esxiGuestIDFor(n.OS),
		}
		nodes = append(nodes, nv)
	}

	doc := map[string]any{
		"vsphere_server":   esxiServerFromEndpoint(o.Inventory.Target.Endpoint),
		"vsphere_user":     defaultStr(o.Inventory.Target.Username, "root"),
		"vsphere_password": o.Inventory.Target.Password,
		"tls_insecure":     o.Inventory.Target.TLSInsecure,
		"datastore":        o.Inventory.Target.Datastore,
		"iso_datastore":    defaultStr(o.Inventory.Target.ISODatastore, o.Inventory.Target.Datastore),
		"network":          defaultStr(o.Inventory.Target.Network, "VM Network"),
		"nodes":            nodes,
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
// lists on the node; SIZE comes from cluster-level Ceph defaults. Order:
// data disks first, then DB, then WAL.
//
// Example with cluster defaults data=2048, db=100, wal=0 and a node with
// data_devices=[/dev/sdb,/dev/sdc], db_devices=[/dev/sdd]:
//   → 3 extra disks: [2048, 2048, 100]
//
// Backward compat: if data/db/wal lists are empty but legacy
// storage_devices is set, treat the first entry as data and the rest
// as DB devices, sized from cluster defaults.
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

	dataSize := c.OSDDataDiskSizeGB
	if dataSize == 0 {
		dataSize = 2048
	}
	dbSize := c.OSDDBDiskSizeGB
	if dbSize == 0 {
		dbSize = 100
	}
	walSize := c.OSDWALDiskSizeGB

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
