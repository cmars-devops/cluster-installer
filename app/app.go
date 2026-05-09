package main

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/cmars-devops/cluster-installer/internal/content"
	"github.com/cmars-devops/cluster-installer/internal/inventory"
	"github.com/cmars-devops/cluster-installer/internal/logging"
	"github.com/cmars-devops/cluster-installer/internal/run"
	"github.com/cmars-devops/cluster-installer/internal/runner/esxi"
	"github.com/cmars-devops/cluster-installer/internal/runtime"
	"github.com/cmars-devops/cluster-installer/internal/state"
)

// App is the Wails-bound singleton. Every method here is callable from the
// Svelte frontend via the generated wailsjs bindings.
type App struct {
	ctx      context.Context
	binaries embed.FS
	log      *logging.Logger
	store    *state.Store

	// runCancels tracks the cancel function for each in-flight ApplyRun so
	// CancelRun(runID) can interrupt the pipeline. Protected by mu.
	mu         sync.Mutex
	runCancels map[string]context.CancelFunc
}

func NewApp(binaries embed.FS) *App {
	return &App{
		binaries:   binaries,
		log:        logging.New(),
		store:      state.New(),
		runCancels: make(map[string]context.CancelFunc),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.log.Info("startup", "msg", "extracting embedded binaries")
	if err := runtime.ExtractEmbeddedBinaries(a.binaries); err != nil {
		a.log.Error("startup", "err", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.log.Info("shutdown", "runs", a.store.OpenRunCount())
	// Wipe per-run ISO uploads from every ESXi datastore the wizard
	// touched. The install media is single-use — keeping it around
	// just consumes datastore space and confuses operators who later
	// inspect the datastore tree. Best-effort: shutdown shouldn't
	// hang on network hiccups, so each call uses a tight timeout and
	// logs+continues on any failure.
	a.cleanupRunISOs(ctx)
}

// cleanupRunISOs deletes the per-run staging directory from every
// run's iso_datastore. Goes through state.Store.List so we wipe even
// runs the user never resumed (orphaned uploads from cancelled or
// failed Apply attempts).
func (a *App) cleanupRunISOs(parentCtx context.Context) {
	runs, err := a.store.List()
	if err != nil {
		a.log.Warn("shutdown.cleanup", "err", err)
		return
	}
	for _, rs := range runs {
		r, err := a.store.Load(rs.ID)
		if err != nil || r.Inventory.Target.Type != "esxi" {
			continue
		}
		ds := r.Inventory.Target.ISODatastore
		if ds == "" {
			ds = r.Inventory.Target.Datastore
		}
		if ds == "" && len(r.Inventory.Nodes) > 0 {
			ds = r.Inventory.Nodes[0].Datastore
		}
		if ds == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
		c, err := esxi.NewClient(ctx, r.Inventory.Target)
		if err != nil {
			a.log.Warn("shutdown.cleanup.connect", "run", r.ID, "err", err)
			cancel()
			continue
		}
		dsRel := "cluster-installer/" + r.ID
		res := c.DeleteDatastoreDir(ctx, ds, dsRel, func(line string) {
			a.log.Info("shutdown.cleanup", "run", r.ID, "line", line)
		})
		if len(res.StillPresent) > 0 || len(res.Errors) > 0 {
			a.log.Warn("shutdown.cleanup.result",
				"run", r.ID,
				"still_present", res.StillPresent,
				"errors", res.Errors,
			)
		}
		c.Close(ctx)
		cancel()
	}
}

// ---- Methods exposed to the frontend ----------------------------------

// CheckRuntime verifies that uv + ansible-core are present and usable. If not,
// it bootstraps them in %LOCALAPPDATA%\cluster-installer\runtime\.
func (a *App) CheckRuntime() (runtime.Status, error) {
	return runtime.EnsureReady(a.ctx, a.log)
}

// FetchContent clones (or pulls) the content repo at the given git ref into
// %LOCALAPPDATA%\cluster-installer\content\<ref>\ and returns the local path.
func (a *App) FetchContent(repo, ref string) (string, error) {
	return content.Fetch(a.ctx, repo, ref, a.log)
}

// ValidateInventory checks the wizard's draft YAML against
// content/schema/inventory.schema.json.
func (a *App) ValidateInventory(yamlText string, contentDir string) (inventory.ValidationResult, error) {
	return inventory.ValidateYAML(yamlText, contentDir)
}

// PlanRun produces a human-readable preview of what ApplyRun would do.
//
// Trade-off: a full `terraform plan` requires the embedded HTTP server,
// downloaded ISOs, datastore uploads (ESXi), and ~50 MB of provider
// downloads — all side effects that don't belong in a "preview". So this
// renders a plain-text summary derived from the inventory: stage list,
// per-node resource shape, target endpoint, and the sources Apply will
// reach. The actual terraform plan runs as part of ApplyRun.
func (a *App) PlanRun(runID string) (string, error) {
	r, err := a.store.Load(runID)
	if err != nil {
		return "", fmt.Errorf("load run: %w", err)
	}
	inv := r.Inventory

	stages := stagesForTopology(inv.Cluster.Topology, inv.Target.Type)

	var b strings.Builder
	fmt.Fprintf(&b, "Cluster:        %s.%s\n", inv.Cluster.Name, inv.Cluster.Domain)
	fmt.Fprintf(&b, "Topology:       %s\n", inv.Cluster.Topology)
	fmt.Fprintf(&b, "Target:         %s @ %s\n", inv.Target.Type, inv.Target.Endpoint)
	if inv.Target.Type == "esxi" {
		fmt.Fprintf(&b, "  datastore:    %s\n", inv.Target.Datastore)
		fmt.Fprintf(&b, "  iso store:    %s\n", defaultStr(inv.Target.ISODatastore, inv.Target.Datastore))
		fmt.Fprintf(&b, "  network:      %s\n", inv.Target.Network)
	}
	fmt.Fprintf(&b, "Kubernetes:     %s %s (cni=%s)\n",
		inv.Cluster.Kubernetes.Distro, inv.Cluster.Kubernetes.Version, inv.Cluster.Kubernetes.CNI)
	fmt.Fprintf(&b, "Nodes:          %d\n", len(inv.Nodes))
	for _, n := range inv.Nodes {
		extra := ""
		if n.NeedsCephOSD() && len(n.DataDevices) > 0 {
			extra = fmt.Sprintf("  data=%v", n.DataDevices)
		}
		clusterIP := ""
		if n.ClusterIP != "" {
			clusterIP = " cnet=" + n.ClusterIP
		}
		fmt.Fprintf(&b, "  - %-20s %s%s  os=%-10s cpu=%d mem=%dG disk=%dG roles=%v%s\n",
			n.Hostname, n.IP, clusterIP, n.OS, defaultI(n.CPU, 2),
			defaultI(n.MemoryGB, 4), defaultI(n.DiskGB, 40), n.Roles, extra)
	}
	if inv.Cluster.Topology != "k8s-only" {
		fmt.Fprintf(&b, "Ceph:           public=%s cluster=%s replica=%d failure_domain=%s\n",
			inv.Ceph.PublicNetwork, inv.Ceph.ClusterNetwork,
			defaultI(inv.Ceph.Replication, 3),
			defaultStr(inv.Ceph.FailureDomain, "host"))
	}
	fmt.Fprintf(&b, "Content:        %s @ %s\n", inv.Content.Repo, inv.Content.Ref)
	fmt.Fprintf(&b, "\nApply will run %d stage(s):\n", len(stages))
	for i, s := range stages {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
	}
	fmt.Fprintf(&b, "\nNote: this preview is derived from the inventory. The full\n")
	fmt.Fprintf(&b, "`terraform plan` runs as part of Apply (it requires downloaded\n")
	fmt.Fprintf(&b, "ISOs and the embedded HTTP server, which are side-effects we\n")
	fmt.Fprintf(&b, "deliberately avoid before user consent).\n")
	return b.String(), nil
}

func stagesForTopology(topology, target string) []string {
	base := []string{"seed_iso"}
	if target == "esxi" {
		base = append(base, "datastore_upload")
	}
	base = append(base, "terraform_init", "terraform_plan", "terraform_apply", "wait_ssh", "preflight")
	switch topology {
	case "ceph-only":
		base = append(base, "ceph")
	case "k8s-only":
		base = append(base, "kubernetes", "addons")
	default:
		base = append(base, "ceph", "kubernetes", "csi", "addons")
	}
	return base
}

func defaultStr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}
func defaultI(n, dflt int) int {
	if n == 0 {
		return dflt
	}
	return n
}

// ApplyRun executes the full pipeline:
//   1. Bind embedded HTTP server on the Windows NIC reachable to the target.
//   2. Render Agama profiles (HTTP-served) + Combustion ISOs (CD-attach) into
//      a per-run staging directory.
//   3. terraform apply (libvirt/Proxmox) — VMs boot, fetch profiles, install OS.
//   4. SSH wait → Ansible 00→40 → finalize.
//
// Streams progress to the Svelte frontend via Wails events:
//   - run:server-listening  { url: "http://10.10.1.99:54321" }
//   - run:firewall-hint     { url, note }
//   - run:stage             stage name
//   - run:line              one log line
func (a *App) ApplyRun(runID string) error {
	r, err := a.store.Load(runID)
	if err != nil {
		return fmt.Errorf("load run: %w", err)
	}
	contentDir := runtime.ContentDir() + "/" + r.Inventory.Content.Ref
	o := &run.Orchestrator{
		Run:        &r,
		ContentDir: contentDir,
		Inventory:  r.Inventory,
		Store:      a.store,
		Log:        a.log,
		Events:     wailsEmitter{ctx: a.ctx},
	}

	// Per-run cancellable context — derived from the Wails app ctx so that
	// quitting the app still tears the pipeline down, but CancelRun(runID)
	// can stop a single run without killing the whole UI.
	runCtx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.runCancels[runID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.runCancels, runID)
		a.mu.Unlock()
		cancel()
	}()

	return o.Apply(runCtx)
}

// CancelRun interrupts an in-flight ApplyRun. The orchestrator detects the
// ctx cancellation at the next stage boundary, terraform/ansible child
// processes are killed, and the run is marked StageFailed with
// "cancelled by user" so the wizard can offer Resume from the same point.
//
// CancelRun does NOT terraform-destroy half-created VMs — that's gated by a
// separate explicit user action because partial VMs may still hold useful
// state (logs, half-installed OS) the operator wants to inspect.
func (a *App) CancelRun(runID string) error {
	a.mu.Lock()
	cancel, ok := a.runCancels[runID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("run %s is not currently executing", runID)
	}
	a.log.Info("cancel", "run", runID)
	cancel()
	return nil
}

// wailsEmitter adapts Wails's EventsEmit to run.Emitter.
type wailsEmitter struct{ ctx context.Context }

func (w wailsEmitter) Emit(name string, data ...interface{}) {
	wailsruntime.EventsEmit(w.ctx, name, data...)
}

// CreateRun starts a new run from a validated inventory.
func (a *App) CreateRun(inv inventory.Inventory) (string, error) {
	return a.store.NewRun(inv)
}

// ResumeRun loads an existing run by ID.
func (a *App) ResumeRun(runID string) (state.Run, error) {
	return a.store.Load(runID)
}

// ListRuns returns the recent runs for the dashboard.
func (a *App) ListRuns() ([]state.RunSummary, error) {
	return a.store.List()
}

// GetRun returns the full Run document — used by Step 6 / Step 7 to
// render verify results (the per-check pass/fail + detail) and other
// post-completion state that doesn't fit on the lighter RunSummary.
func (a *App) GetRun(runID string) (state.Run, error) {
	return a.store.Load(runID)
}

// RedeployDevVM tears down and re-runs the same dev-vm inventory: a quick
// iterate-on-the-unattended-install loop. Refuses to run for cluster
// topologies because terraform destroy on a half-made cluster is far
// more disruptive than what users expect from this button.
//
// v1: triggers ApplyRun again on the same run. Terraform's apply-after-
// apply with the same state reconciles the VM in-place; for a true reset
// the operator deletes the VM in vSphere first.
func (a *App) RedeployDevVM(runID string) error {
	r, err := a.store.Load(runID)
	if err != nil {
		return fmt.Errorf("load run: %w", err)
	}
	if r.Inventory.Cluster.Topology != inventory.TopologyDevVM {
		return fmt.Errorf("RedeployDevVM is only available for topology=dev-vm runs")
	}
	return a.ApplyRun(runID)
}

// LeftoverISOs is the structured response from ListLeftoverISOs —
// frontend renders this in Step 7's diagnostics panel.
type LeftoverISOs struct {
	Datastore string               `json:"datastore"`
	Entries   []esxi.LeftoverEntry `json:"entries"`
	TotalGB   float64              `json:"total_gb"`
	Error     string               `json:"error,omitempty"`
}

// ourRunIDs returns the set of run-ids the wizard has a local run.json
// for. Used to mark Step 7 leftover entries as Owned and to gate
// WipeLeftoverISOs to only delete things we created.
func (a *App) ourRunIDs() map[string]bool {
	out := map[string]bool{}
	rs, err := a.store.List()
	if err != nil {
		return out
	}
	for _, r := range rs {
		out[r.ID] = true
	}
	return out
}

// ListLeftoverISOs scans `cluster-installer/` on the run's ISO
// datastore and returns every run-id directory still present, with
// the ISO files inside each. Each entry is tagged Owned=true if the
// run-id matches one of OUR local runs, false otherwise.
//
// Used by Step 7's diagnostic panel so the operator can see exactly
// what's piling up — and crucially, distinguish "ours" (safe to
// wipe) from "unknown" (leave alone).
func (a *App) ListLeftoverISOs(target inventory.TargetSpec) LeftoverISOs {
	out := LeftoverISOs{}
	ds := target.ISODatastore
	if ds == "" {
		ds = target.Datastore
	}
	if ds == "" {
		out.Error = "no datastore set in target — fill Step 2 first"
		return out
	}
	out.Datastore = ds
	if target.Type != "esxi" {
		out.Error = "only esxi target supported"
		return out
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	c, err := esxi.NewClient(ctx, target)
	if err != nil {
		out.Error = fmt.Sprintf("connect ESXi: %v", err)
		return out
	}
	defer c.Close(ctx)
	entries, err := c.ListClusterInstallerEntries(ctx, ds, a.ourRunIDs())
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var total int64
	for _, e := range entries {
		for _, f := range e.Files {
			total += f.Size
		}
	}
	out.Entries = entries
	out.TotalGB = float64(total) / (1024 * 1024 * 1024)
	return out
}

// WipeLeftoverISOs deletes ONLY OUR cluster-installer/<run-id>/ trees
// on the target's ISO datastore. "Ours" = run-ids the wizard has a
// local run.json for. Unknown run-ids on the datastore are left
// strictly alone — could be another operator's data, a parallel
// installer instance, etc. Streams progress via "cleanup:line".
//
// Earlier behaviour was a blanket wipe of every child of
// cluster-installer/; the operator (rightly) flagged that as unsafe.
// This is the surgical replacement.
func (a *App) WipeLeftoverISOs(target inventory.TargetSpec) error {
	ds := target.ISODatastore
	if ds == "" {
		ds = target.Datastore
	}
	if ds == "" {
		return fmt.Errorf("no datastore set in target")
	}
	if target.Type != "esxi" {
		return fmt.Errorf("only esxi target supported")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	c, err := esxi.NewClient(ctx, target)
	if err != nil {
		return fmt.Errorf("connect ESXi: %w", err)
	}
	defer c.Close(ctx)
	emit := func(line string) {
		wailsruntime.EventsEmit(a.ctx, "cleanup:line", line)
	}
	n, err := c.WipeOwnedRunDirs(ctx, ds, a.ourRunIDs(), emit)
	if err != nil {
		return err
	}
	a.log.Info("wipe.leftovers", "datastore", ds, "owned_wiped", n)
	return nil
}

// DiscoverESXi connects to the supplied ESXi/vCenter target and returns the
// resource catalog the wizard's Step 2 displays as dropdowns (datastores,
// networks, host info). Errors are encoded into the Discovery struct
// rather than thrown so the frontend can render them inline.
func (a *App) DiscoverESXi(target inventory.TargetSpec) esxi.Discovery {
	a.log.Info("esxi.discover", "endpoint", target.Endpoint, "user", target.Username)
	return esxi.Discover(a.ctx, target)
}
