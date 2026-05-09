// Package seed renders per-node first-boot configs (Agama / Combustion+
// Ignition) and packs them into a tiny ISO9660 image with the right volume
// label, ready to attach to the VM as a second CD-ROM (Combustion only)
// or to be served over HTTP by internal/httpserve (Agama).
//
// IMPORTANT: Agama (openSUSE Leap 16+) does NOT support inst.auto=device://
// — only inst.auto=http://... — so for Leap/Tumbleweed nodes the rendered
// JSON profile is served from the embedded HTTP server, not packed into
// an OEMDRV ISO. The OEMDRV ISO path remains for AutoYaST fallback only.
//
// Reference: P:\K3s@IDC docs/01.OpenSUSE16+Ceph-Setup/04-troubleshooting-lessons.md issue #2
package seed

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// Context is what every seed template sees.
type Context struct {
	Cluster ClusterContext
	Network NetworkContext
	Node    NodeContext
}

type ClusterContext struct {
	Name            string
	Domain          string
	Timezone        string
	Username        string   // sudo account autoinstall creates (default "triangles")
	HostsEntries    []string // e.g. "10.10.1.31  k3s-server-01"
	NodePassword    string   // cluster-wide node password (plain — applied via late-commands chpasswd)
	SSHImportGitHub []string // GitHub usernames; ssh-import-id-gh fetches keys at first boot
}

type NetworkContext struct {
	PodCIDR          string
	ServiceCIDR      string
	Gateway          string
	Nameservers      []string
	PrefixLen        int
	ClusterPrefixLen int
}

// NodeContext is a flattened view tailored for templates.
type NodeContext struct {
	Hostname           string
	IP                 string
	ClusterIP          string   // Ceph cluster network IP (C-Net), empty if single-NIC
	NetworkInterface   string
	PrimaryMAC         string   // mac-address used for NM connection binding
	SSHAuthorizedKeys  []string
	RootPasswordHash   string   // SHA-512 crypt hash
	Roles              []string
	OS                 string
	NeedsCeph          bool
	NeedsCephOSD       bool
	HasClusterNIC      bool
	NeedsK3sSELinux    bool
}

// RenderFile renders a single template file with the given context.
func RenderFile(tmplPath string, ctx Context) ([]byte, error) {
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		return nil, err
	}
	t, err := template.New(filepath.Base(tmplPath)).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", tmplPath, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("render %s: %w", tmplPath, err)
	}
	return buf.Bytes(), nil
}

// BuildContext is a convenience adapter from inventory → seed Context.
// rootPwHash and hostsEntries are derived per run; the caller provides them.
func BuildContext(inv inventory.Inventory, node inventory.NodeSpec, rootPwHash string, hostsEntries []string) Context {
	iface := node.NetworkIface
	if iface == "" {
		iface = "ens192" // ESXi vmxnet3 default; libvirt virtio_net is usually eth0/enp1s0
	}
	tz := inv.Cluster.Timezone
	if tz == "" {
		tz = "Asia/Seoul"
	}
	ns := inv.Network.Nameservers
	if len(ns) == 0 {
		ns = []string{"1.1.1.1", "8.8.8.8"}
	}
	prefix := inv.Network.PrefixLen
	if prefix == 0 {
		prefix = 24
	}
	clusterPrefix := inv.Network.ClusterPrefixLen
	if clusterPrefix == 0 {
		clusterPrefix = 24
	}
	return Context{
		Cluster: ClusterContext{
			Name:            inv.Cluster.Name,
			Domain:          inv.Cluster.Domain,
			Timezone:        tz,
			Username:        inv.ClusterAuth.SudoUser(),
			HostsEntries:    hostsEntries,
			NodePassword:    inv.ClusterAuth.NodePassword,
			SSHImportGitHub: inv.ClusterAuth.SSHImportGitHub,
		},
		Network: NetworkContext{
			PodCIDR:          inv.Network.PodCIDR,
			ServiceCIDR:      inv.Network.ServiceCIDR,
			Gateway:          inv.Network.Gateway,
			Nameservers:      ns,
			PrefixLen:        prefix,
			ClusterPrefixLen: clusterPrefix,
		},
		Node: NodeContext{
			Hostname:         node.Hostname,
			IP:               node.IP,
			ClusterIP:        node.ClusterIP,
			NetworkInterface: iface,
			PrimaryMAC:       node.PrimaryMAC,
			// Per-node keys win; otherwise the cluster-wide list from
			// Step 1 ("노드 인증 자격 증명") fills in.
			SSHAuthorizedKeys: firstNonEmpty(node.SSHAuthKeys, inv.ClusterAuth.SSHAuthorizedKeys),
			RootPasswordHash:  rootPwHash,
			Roles:             node.Roles,
			OS:                node.OS,
			NeedsCeph:         node.NeedsCeph(),
			NeedsCephOSD:      node.NeedsCephOSD(),
			HasClusterNIC:     node.HasClusterNIC(),
			NeedsK3sSELinux:   node.NeedsK3sSELinux(),
		},
	}
}

func firstNonEmpty(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

// HostsEntriesFromInventory generates the cluster-wide /etc/hosts block
// (every node + cluster-network alias for Ceph OSDs).
func HostsEntriesFromInventory(inv inventory.Inventory) []string {
	var out []string
	for _, n := range inv.Nodes {
		out = append(out, fmt.Sprintf("%s\t%s", n.IP, n.Hostname))
	}
	// Cluster-network aliases for OSDs (e.g. ceph-osd-01-cnet).
	for _, n := range inv.Nodes {
		if n.ClusterIP != "" {
			out = append(out, fmt.Sprintf("%s\t%s-cnet", n.ClusterIP, n.Hostname))
		}
	}
	return out
}
