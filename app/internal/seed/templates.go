// Package seed renders per-node first-boot configs (AutoYaST / Combustion+
// Ignition) and packs them into a tiny ISO9660 image with the right volume
// label, ready to attach to the VM as a second CD-ROM.
package seed

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/triangles-co-kr/cluster-installer/internal/inventory"
)

// Context is what every seed template sees.
type Context struct {
	Cluster inventory.ClusterSpec
	Network NetworkContext
	Node    NodeContext
}

type NetworkContext struct {
	PodCIDR     string
	ServiceCIDR string
	Gateway     string
	DNS         string
	PrefixLen   int
}

// NodeContext is a flattened view tailored for templates.
type NodeContext struct {
	Hostname           string
	IP                 string
	NetworkInterface   string
	SSHAuthorizedKeys  []string
	Roles              []string
	OS                 string
	NeedsCeph          bool
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
func BuildContext(inv inventory.Inventory, node inventory.NodeSpec) Context {
	iface := node.NetworkIface
	if iface == "" {
		iface = "eth0"
	}
	return Context{
		Cluster: inv.Cluster,
		Network: NetworkContext{
			PodCIDR:     inv.Network.PodCIDR,
			ServiceCIDR: inv.Network.ServiceCIDR,
			Gateway:     inv.Network.Gateway,
			DNS:         inv.Network.DNS,
			PrefixLen:   inv.Network.PrefixLen,
		},
		Node: NodeContext{
			Hostname:          node.Hostname,
			IP:                node.IP,
			NetworkInterface:  iface,
			SSHAuthorizedKeys: node.SSHAuthKeys,
			Roles:             node.Roles,
			OS:                node.OS,
			NeedsCeph:         node.NeedsCeph(),
		},
	}
}
