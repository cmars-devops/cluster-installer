package main

import (
	"context"
	"embed"
	"fmt"
	"sync"

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

// PlanRun produces a human-readable preview of what would happen on apply.
// Wraps `terraform plan` and pipeline planning.
func (a *App) PlanRun(runID string) (string, error) {
	return "", fmt.Errorf("PlanRun: not implemented")
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

// DiscoverESXi connects to the supplied ESXi/vCenter target and returns the
// resource catalog the wizard's Step 2 displays as dropdowns (datastores,
// networks, host info). Errors are encoded into the Discovery struct
// rather than thrown so the frontend can render them inline.
func (a *App) DiscoverESXi(target inventory.TargetSpec) esxi.Discovery {
	a.log.Info("esxi.discover", "endpoint", target.Endpoint, "user", target.Username)
	return esxi.Discover(a.ctx, target)
}
