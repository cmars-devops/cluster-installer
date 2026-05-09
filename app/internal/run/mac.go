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
// given (cluster, hostname, nicIndex) tuple on the given target type.
// Format: "AA:BB:CC:DD:EE:FF" (uppercase). nicIndex=0 reproduces the
// pre-multi-NIC behaviour byte-for-byte (so existing single-NIC nodes
// keep their MACs across the upgrade).
func allocateMAC(targetType, cluster, hostname string, nicIndex int) string {
	oui := targetOUI(targetType)
	key := cluster + "/" + hostname
	if nicIndex > 0 {
		// Suffix only when index > 0 so single-NIC inventories keep
		// the same MAC they had before the multi-NIC change.
		key = fmt.Sprintf("%s#nic%d", key, nicIndex)
	}
	sum := sha256.Sum256([]byte(key))
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

// ensureNodeMACs walks the inventory and fills in MAC addresses for
// every NIC that doesn't already carry one. Behaviour:
//
//   - NodeSpec.PrimaryMAC (legacy single-NIC field) is set if empty —
//     remains the canonical MAC for the FIRST NIC.
//   - NodeSpec.NICs (multi-NIC dev-vm flow) gets a per-entry MAC.
//     Entry 0 mirrors PrimaryMAC; entry N>0 hashes a "nicN"-suffixed
//     key so each NIC gets its own deterministic address.
//   - Cluster mode with cluster_ip set on a node + target.ClusterNetwork
//     configured: a SECOND NIC entry is materialized (network =
//     target.ClusterNetwork, ip = cluster_ip, MAC = hash#nic1) so the
//     same downstream code (tfvars, netplan rewriter) treats it as
//     just another NIC entry. Without target.ClusterNetwork the
//     materialization is skipped — cluster_ip is silently ignored
//     because we have no port-group to attach the NIC to.
//
// Idempotent: re-running on a populated inventory makes no changes.
// Returns true if any field was filled.
func ensureNodeMACs(inv *inventory.Inventory) bool {
	changed := false
	for i := range inv.Nodes {
		n := &inv.Nodes[i]
		// Always make sure PrimaryMAC is populated — used by legacy
		// single-NIC paths and by NICs[0] when the operator left
		// the multi-NIC list at default.
		if n.PrimaryMAC == "" {
			n.PrimaryMAC = allocateMAC(inv.Target.Type, inv.Cluster.Name, n.Hostname, 0)
			changed = true
		}
		// Materialize the Ceph cluster NIC once, when the inventory
		// asks for it (cluster_ip set) AND the operator configured a
		// port-group (target.cluster_network). Both required — without
		// a port-group there's nothing to attach the NIC to, so we
		// silently skip rather than create a half-configured NIC.
		// Idempotent: only adds when n.NICs is empty (otherwise the
		// operator already curated the list).
		if n.ClusterIP != "" && inv.Target.ClusterNetwork != "" && len(n.NICs) == 0 {
			n.NICs = []inventory.NICSpec{
				{
					Network: inv.Target.Network,
					IPMode:  n.IPMode,
					IP:      n.IP,
					Label:   "primary",
				},
				{
					Network: inv.Target.ClusterNetwork,
					IPMode:  "static",
					IP:      n.ClusterIP,
					Label:   "cluster",
				},
			}
			changed = true
		}
		// Fill MACs on each NIC entry. NIC[0] inherits PrimaryMAC so
		// the two views agree even when the operator only edited one
		// of them.
		for j := range n.NICs {
			if n.NICs[j].MAC != "" {
				continue
			}
			if j == 0 {
				n.NICs[j].MAC = n.PrimaryMAC
			} else {
				n.NICs[j].MAC = allocateMAC(inv.Target.Type, inv.Cluster.Name, n.Hostname, j)
			}
			changed = true
		}
	}
	return changed
}
