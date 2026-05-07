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
	"time"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
	// TODO(phase 2): add to go.mod via `go mod tidy`:
	//   github.com/vmware/govmomi
	//   github.com/vmware/govmomi/find
	//   github.com/vmware/govmomi/object
	//   github.com/vmware/govmomi/vim25/mo
	//   github.com/vmware/govmomi/vim25/types
)

// Discovery is the JSON-friendly shape returned to the Wails frontend.
// Field names match frontend lib/api.ts ESXiDiscovery exactly.
type Discovery struct {
	OK         bool             `json:"ok"`
	Error      string           `json:"error,omitempty"`
	Host       *HostInfo        `json:"host,omitempty"`
	Datastores []DatastoreInfo  `json:"datastores,omitempty"`
	Networks   []NetworkInfo    `json:"networks,omitempty"`
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
func Discover(ctx context.Context, t inventory.TargetSpec) Discovery {
	// 1. Validate inputs early.
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

	// 2. Normalize the URL govmomi expects (host:port + /sdk path).
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return Discovery{Error: fmt.Sprintf("parse endpoint: %v", err)}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/sdk"
	}
	u.User = url.UserPassword(t.Username, t.Password)

	// 3. TODO(phase 2): replace this stub with real govmomi probe.
	//
	//   ctx, cancel := context.WithTimeout(ctx, 15*time.Second); defer cancel()
	//   c, err := govmomi.NewClient(ctx, u, t.TLSInsecure)
	//   if err != nil { return Discovery{Error: ...} }
	//   defer c.Logout(ctx)
	//
	//   // Determine HostAgent vs VirtualCenter from c.Client.ServiceContent.About.ApiType
	//   about := c.Client.ServiceContent.About
	//
	//   finder := find.NewFinder(c.Client, true)
	//   dc, _ := finder.DefaultDatacenter(ctx)
	//   finder.SetDatacenter(dc)
	//
	//   dsList, _ := finder.DatastoreList(ctx, "*")
	//   var dsRefs []types.ManagedObjectReference
	//   for _, ds := range dsList { dsRefs = append(dsRefs, ds.Reference()) }
	//   var dsProps []mo.Datastore
	//   c.Client.RetrieveOne(ctx, dsRefs, []string{"name","summary"}, &dsProps)
	//   …
	//
	//   netList, _ := finder.NetworkList(ctx, "*")
	//   …
	//
	// Until govmomi is in go.mod, this returns a clear "not implemented"
	// signal with timing data so the frontend test button still produces
	// useful feedback.
	_ = time.Now()
	return Discovery{
		Error: "Phase 2: ESXi discovery requires the govmomi adapter — " +
			"see docs/phase-1-open-items.md §3. The wizard will surface " +
			"realistic mock data in dev mode (browser-only) until then.",
	}
}
