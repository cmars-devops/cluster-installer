// Deterministic MAC pre-allocation. Runs before Terraform so the VM's MAC
// is known at seed-render time — the Agama post-install script bakes it
// into /etc/NetworkManager/system-connections/public.nmconnection so
// adding a second NIC later cannot rebind the public connection (issue #5
// in docs/lessons-from-IDC.md).
//
// MACs are derived as sha256(cluster_name + "/" + hostname)[:3] suffixed
// onto a per-target locally-administered OUI:
//
//	libvirt → 52:54:00:xx:xx:xx (KVM/QEMU's reserved range)
//	proxmox → BC:24:11:xx:xx:xx (Proxmox VE's documented prefix)
//	esxi    → 00:50:56:xx:xx:xx (VMware's reserved manual range; high
//	          byte must be 0x00–0x3F to avoid collision with auto-MACs)
//
// The hash makes re-runs idempotent: same cluster + hostname → same MAC,
// so a destroy+apply cycle reuses the address and DHCP reservations stay
// valid. To rotate MACs intentionally, change the cluster name.
package run

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// targetOUI maps target.type to the OUI prefix we use for the first three
// MAC bytes. An unknown target falls back to the libvirt OUI — safe for
// any KVM-derived hypervisor.
func targetOUI(targetType string) [3]byte {
	switch strings.ToLower(targetType) {
	case "proxmox":
		return [3]byte{0xBC, 0x24, 0x11}
	case "esxi":
		return [3]byte{0x00, 0x50, 0x56}
	default: // libvirt + fallback
		return [3]byte{0x52, 0x54, 0x00}
	}
}

// allocateMAC returns a deterministic, locally-administered MAC for the
// given (cluster, hostname) pair on the given target type. Format:
// "AA:BB:CC:DD:EE:FF" (uppercase, colon-separated).
func allocateMAC(targetType, cluster, hostname string) string {
	oui := targetOUI(targetType)
	sum := sha256.Sum256([]byte(cluster + "/" + hostname))
	suffix := sum[:3]

	// ESXi requires the high byte of the suffix to be ≤ 0x3F so the full
	// MAC stays inside VMware's manually-assignable range. Mask to 6 bits.
	if oui == ([3]byte{0x00, 0x50, 0x56}) {
		suffix[0] &= 0x3F
	}

	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		oui[0], oui[1], oui[2],
		suffix[0], suffix[1], suffix[2])
}

// ensureNodeMACs walks the inventory and fills in PrimaryMAC for any node
// that doesn't already have one. Returns true if any node was changed,
// so the caller can persist the updated inventory back to run.json.
func ensureNodeMACs(inv *inventory.Inventory) bool {
	changed := false
	for i := range inv.Nodes {
		if inv.Nodes[i].PrimaryMAC != "" {
			continue
		}
		inv.Nodes[i].PrimaryMAC = allocateMAC(
			inv.Target.Type,
			inv.Cluster.Name,
			inv.Nodes[i].Hostname,
		)
		changed = true
	}
	return changed
}
