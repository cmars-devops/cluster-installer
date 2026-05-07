package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/triangles-co-kr/cluster-installer/internal/content"
	"github.com/triangles-co-kr/cluster-installer/internal/inventory"
	"github.com/triangles-co-kr/cluster-installer/internal/logging"
	"github.com/triangles-co-kr/cluster-installer/internal/runtime"
	"github.com/triangles-co-kr/cluster-installer/internal/state"
)

// App is the Wails-bound singleton. Every method here is callable from the
// Svelte frontend via the generated wailsjs bindings.
type App struct {
	ctx      context.Context
	binaries embed.FS
	log      *logging.Logger
	store    *state.Store
}

func NewApp(binaries embed.FS) *App {
	return &App{
		binaries: binaries,
		log:      logging.New(),
		store:    state.New(),
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

// ApplyRun executes the full pipeline (TF apply → SSH wait → Ansible 00→40).
// Streams events back to the frontend via runtime.EventsEmit.
func (a *App) ApplyRun(runID string) error {
	return fmt.Errorf("ApplyRun: not implemented")
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
