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
	Name          string `json:"name"`
	MemoryMB      int    `json:"memory_mb"`
	VCPU          int    `json:"vcpu"`
	DiskGB        int    `json:"disk_gb"`
	ExtraDisksGB  []int  `json:"extra_disks_gb"`
	SeedISOPath   string `json:"seed_iso_path"`
	MAC           string `json:"mac,omitempty"`
	Pool          string `json:"pool,omitempty"`         // libvirt storage pool override (per-node)
	DiskFormat    string `json:"disk_format,omitempty"`  // "qcow2" (thin) or "raw" (thick)
	BootMode      string `json:"boot_mode"`              // "kernel" (Agama) or "iso" (Combustion)
	KernelPath    string `json:"kernel_path,omitempty"`
	InitrdPath    string `json:"initrd_path,omitempty"`
	Cmdline       string `json:"cmdline,omitempty"`
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
		// ESXi backend (govmomi) is a v2 milestone. The inventory is
		// captured + saved, but Apply will not run terraform yet — the
		// orchestrator returns a friendly error here pointing at the docs.
		return "", fmt.Errorf(
			"ESXi target captured but not yet supported by the run engine — " +
				"see docs/phase-1-open-items.md §3 for the implementation plan " +
				"(govmomi adapter + per-cluster ISO remaster)")
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
			ExtraDisksGB: extraDisksFor(n),
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
		case "leap", "tumbleweed":
			nv.BootMode = "kernel"
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
		"libvirt_uri":    o.Inventory.Target.Endpoint,
		"pool":           "default",
		"network_id":     "default", // TODO: wizard step 2 should let user pick
		"base_volume_id": baseVolumeIDFor(o.Inventory),
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
			ExtraDisksGB: extraDisksFor(n),
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

func extraDisksFor(n inventory.NodeSpec) []int {
	if !n.HasRole("ceph-osd") {
		return []int{}
	}
	// Default OSD layout from P:\K3s@IDC: 2TB data + 100GB WAL/DB.
	// Operators override via inventory.nodes[].storage_devices in v0.2.
	return []int{2048, 100}
}

func baseVolumeIDFor(inv inventory.Inventory) string {
	// TODO: pull from images.yaml once the orchestrator caches the image.
	// Phase 1 placeholder: assume the operator pre-uploaded a libvirt volume
	// named after the OS family.
	for _, n := range inv.Nodes {
		switch n.OS {
		case "microos":
			return "openSUSE-MicroOS.qcow2"
		case "leap":
			return "openSUSE-Leap-16.0-base.qcow2"
		case "tumbleweed":
			return "openSUSE-Tumbleweed-base.qcow2"
		}
	}
	return ""
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
