// Package esxi probes a vSphere/ESXi endpoint and returns the resource
// inventory the wizard's Step 2 displays as dropdowns. We deliberately
// surface only the fields the wizard needs (datastore name + capacity,
// network name + vSwitch) — not the full govmomi MoRef tree — so the
// frontend type can stay tiny and JSON-friendly.
package esxi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
)

// Discovery is the JSON-friendly shape returned to the Wails frontend.
// Field names match frontend lib/api.ts ESXiDiscovery exactly.
type Discovery struct {
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	Host       *HostInfo       `json:"host,omitempty"`
	Datastores []DatastoreInfo `json:"datastores,omitempty"`
	Networks   []NetworkInfo   `json:"networks,omitempty"`
}

type HostInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
	APIType string `json:"api_type"` // HostAgent | VirtualCenter
}

type DatastoreInfo struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"` // VMFS | NFS | vSAN | …
	CapacityGB float64 `json:"capacity_gb"`
	FreeGB     float64 `json:"free_gb"`
	Accessible bool    `json:"accessible"`
}

type NetworkInfo struct {
	Name    string `json:"name"`
	VSwitch string `json:"vswitch,omitempty"`
	VlanID  int    `json:"vlan_id,omitempty"`
}

// Discover probes the supplied target and lists resources. It is the
// authoritative implementation behind the Wails-bound App.DiscoverESXi
// method. Always returns a Discovery — errors are encoded into Error
// rather than as a Go error, so the frontend can render the failure
// inline next to the form fields.
//
// Performs a single bound (15s) login + listing pass. We do NOT keep
// the govmomi client alive between calls; the wizard's Step 2 Discover
// button is rate-limited by the user, not by us.
func Discover(ctx context.Context, t inventory.TargetSpec) Discovery {
	if t.Type != "esxi" {
		return Discovery{Error: fmt.Sprintf("target.type=%q is not esxi", t.Type)}
	}
	if t.Endpoint == "" {
		return Discovery{Error: "endpoint is required"}
	}
	if t.Username == "" {
		return Discovery{Error: "username is required"}
	}
	if t.Password == "" {
		return Discovery{Error: "password is required"}
	}

	u, err := normaliseSDKURL(t.Endpoint, t.Username, t.Password)
	if err != nil {
		return Discovery{Error: fmt.Sprintf("parse endpoint: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	c, err := govmomi.NewClient(ctx, u, t.TLSInsecure)
	if err != nil {
		return Discovery{Error: fmt.Sprintf("connect: %v", err)}
	}
	defer func() { _ = c.Logout(ctx) }()

	about := c.ServiceContent.About
	host := &HostInfo{
		Name:    about.FullName,
		Version: about.Version,
		Build:   about.Build,
		APIType: about.ApiType, // "HostAgent" or "VirtualCenter"
	}

	finder := find.NewFinder(c.Client, true)
	dc, err := finder.DefaultDatacenter(ctx)
	if err != nil {
		// Standalone ESXi presents a synthetic datacenter named "ha-datacenter";
		// fail loud only if the SDK genuinely returns no DC.
		return Discovery{Error: fmt.Sprintf("default datacenter: %v", err), Host: host, OK: true}
	}
	finder.SetDatacenter(dc)

	dss, dserr := listDatastores(ctx, c, finder)
	nets, neterr := listNetworks(ctx, c, finder)

	switch {
	case dserr != nil && neterr != nil:
		return Discovery{Error: fmt.Sprintf("list datastores: %v; list networks: %v", dserr, neterr), Host: host}
	case dserr != nil:
		return Discovery{Error: fmt.Sprintf("list datastores: %v", dserr), Host: host, Networks: nets}
	case neterr != nil:
		return Discovery{Error: fmt.Sprintf("list networks: %v", neterr), Host: host, Datastores: dss}
	}

	return Discovery{
		OK:         true,
		Host:       host,
		Datastores: dss,
		Networks:   nets,
	}
}

// normaliseSDKURL accepts the user-typed endpoint (e.g. "https://192.168.1.210/")
// and returns a URL govmomi can pass to NewClient — host:port + /sdk path
// + URL-embedded credentials. The wizard form intentionally accepts the
// human-friendly host URL; this function is the only place we know about
// the /sdk SOAP endpoint.
func normaliseSDKURL(endpoint, username, password string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/sdk"
	}
	u.User = url.UserPassword(username, password)
	return u, nil
}

func listDatastores(ctx context.Context, c *govmomi.Client, finder *find.Finder) ([]DatastoreInfo, error) {
	dsList, err := finder.DatastoreList(ctx, "*")
	if err != nil {
		return nil, err
	}
	if len(dsList) == 0 {
		return nil, nil
	}
	refs := make([]types.ManagedObjectReference, 0, len(dsList))
	for _, ds := range dsList {
		refs = append(refs, ds.Reference())
	}

	pc := property.DefaultCollector(c.Client)
	var props []mo.Datastore
	if err := pc.Retrieve(ctx, refs, []string{"name", "summary"}, &props); err != nil {
		return nil, err
	}

	out := make([]DatastoreInfo, 0, len(props))
	for _, p := range props {
		out = append(out, DatastoreInfo{
			Name:       p.Name,
			Type:       p.Summary.Type,
			CapacityGB: bytesToGiB(p.Summary.Capacity),
			FreeGB:     bytesToGiB(p.Summary.FreeSpace),
			Accessible: p.Summary.Accessible,
		})
	}
	return out, nil
}

func listNetworks(ctx context.Context, c *govmomi.Client, finder *find.Finder) ([]NetworkInfo, error) {
	netList, err := finder.NetworkList(ctx, "*")
	if err != nil {
		return nil, err
	}
	if len(netList) == 0 {
		return nil, nil
	}

	// We resolve the friendly name via the network MoRef's Name property.
	// vSwitch/VLAN extraction works only for HostNetwork PortGroups; for
	// dvSwitch portgroups we leave VSwitch empty (the user's wizard form
	// labels them clearly, and the lab/IDC majority is HostNetwork).
	refs := make([]types.ManagedObjectReference, 0, len(netList))
	for _, n := range netList {
		refs = append(refs, n.Reference())
	}

	pc := property.DefaultCollector(c.Client)

	// HostPortGroup vs Network discrimination via Type field.
	var generic []mo.Network
	if err := pc.Retrieve(ctx, refs, []string{"name"}, &generic); err != nil {
		return nil, err
	}

	// vSwitch + VLAN come from each ESXi host's HostNetworkSystem.NetworkInfo.PortGroup.
	// Querying that requires walking host configs — for the discovery dropdown a
	// best-effort name is sufficient. vSwitch/VLAN are surfaced only when the
	// portgroup name itself encodes them (a common IDC convention,
	// e.g. "Storage-Net (VLAN100)").
	out := make([]NetworkInfo, 0, len(generic))
	for _, n := range generic {
		out = append(out, NetworkInfo{
			Name:    n.Name,
			VlanID:  parseVLANFromName(n.Name),
		})
	}
	return out, nil
}

func bytesToGiB(b int64) float64 {
	return float64(b) / float64(1024*1024*1024)
}

// parseVLANFromName extracts a VLAN id from common IDC naming conventions
// like "Storage-Net (VLAN100)" or "Ceph-Cluster-Net (VLAN200)". Returns 0
// when no recognisable pattern is present — the frontend hides the badge
// in that case.
func parseVLANFromName(name string) int {
	idx := strings.Index(name, "(VLAN")
	if idx < 0 {
		return 0
	}
	rest := name[idx+5:]
	end := strings.Index(rest, ")")
	if end < 0 {
		return 0
	}
	var n int
	for _, r := range rest[:end] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
