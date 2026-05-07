// hosts.yml rendering: feed inventory + run secrets into the Go-template
// inventory file under content/ansible/inventory/hosts.yml.tmpl. The
// rendered hosts.yml is what ansible-playbook -i consumes.
package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// inventoryCtx is the context fed to hosts.yml.tmpl. The template adds Helm
// addons + secret references; node-level template helpers (HasRole etc.)
// come from inventory.NodeSpec methods.
type inventoryCtx struct {
	Cluster clusterCtx
	Network networkCtx
	Ceph    cephCtx
	Addons  inventory.AddonsSpec
	Secrets secretsCtx
	Nodes   []inventory.NodeSpec
}

type clusterCtx struct {
	Name       string
	Domain     string
	Kubernetes inventory.K8sSpec
}

type networkCtx struct {
	PodCIDR          string
	ServiceCIDR      string
	VIP              string
	LBPool           string
	IngressLBIP      string
	PrimaryInterface string
}

type cephCtx struct {
	Version        string
	Release        string
	ClusterNetwork string
	RGWRealm       string
}

type secretsCtx struct {
	RKE2Token             string
	K3sToken              string
	CephDashboardPassword string
}

func (o *Orchestrator) renderHostsYAML() (string, error) {
	tmplPath := filepath.Join(o.ContentDir, "ansible", "inventory", "hosts.yml.tmpl")
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		return "", fmt.Errorf("read hosts tmpl: %w", err)
	}
	t, err := template.New("hosts.yml").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse hosts tmpl: %w", err)
	}

	primaryIface := defaultStr(firstNodeInterface(o.Inventory), "ens192")
	ingressIP := o.Inventory.Network.IngressLBIP
	if ingressIP == "" {
		ingressIP = firstAddrFromPool(o.Inventory.Network.LBPool)
	}

	ctx := inventoryCtx{
		Cluster: clusterCtx{
			Name:       o.Inventory.Cluster.Name,
			Domain:     o.Inventory.Cluster.Domain,
			Kubernetes: o.Inventory.Cluster.Kubernetes,
		},
		Network: networkCtx{
			PodCIDR:          o.Inventory.Network.PodCIDR,
			ServiceCIDR:      o.Inventory.Network.ServiceCIDR,
			VIP:              o.Inventory.Network.VIP,
			LBPool:           o.Inventory.Network.LBPool,
			IngressLBIP:      ingressIP,
			PrimaryInterface: primaryIface,
		},
		Ceph: cephCtx{
			Version:        "20.2.1",
			Release:        "tentacle",
			ClusterNetwork: o.Inventory.Ceph.ClusterNetwork,
			RGWRealm:       "triangles",
		},
		Addons: o.Inventory.Addons,
		Secrets: secretsCtx{
			RKE2Token:             o.Run.RKE2Token,
			K3sToken:              o.Run.K3sToken,
			CephDashboardPassword: o.Run.CephDashboardPassword,
		},
		Nodes: o.Inventory.Nodes,
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("render hosts: %w", err)
	}

	dst := filepath.Join(o.runDir(), "ansible", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func firstNodeInterface(inv inventory.Inventory) string {
	for _, n := range inv.Nodes {
		if n.NetworkIface != "" {
			return n.NetworkIface
		}
	}
	return ""
}

func firstAddrFromPool(pool string) string {
	// "10.10.1.40-10.10.1.49" → "10.10.1.40"
	for i := 0; i < len(pool); i++ {
		if pool[i] == '-' {
			return pool[:i]
		}
	}
	return pool
}
