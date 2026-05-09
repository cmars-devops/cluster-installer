// Package state owns the per-run JSON document under
// %LOCALAPPDATA%\cluster-installer\runs\<run-id>\run.json. The wizard reads
// and writes this document to support stop/resume.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
	"github.com/cmars-devops/cluster-installer/internal/runtime"
)

// Stage names map 1:1 to the pipeline phases.
type Stage string

const (
	StagePending   Stage = "pending"
	StageSeedISO   Stage = "seed_iso"
	StageDSUpload  Stage = "datastore_upload" // ESXi-only: push seed ISOs to vSphere datastore
	StageTFInit    Stage = "terraform_init"
	StageTFPlan    Stage = "terraform_plan"
	StageTFApply   Stage = "terraform_apply"
	StageWaitSSH   Stage = "wait_ssh"
	StageVerify    Stage = "verify" // dev-vm only: SSH in, sanity-check OS install
	StagePreflight Stage = "preflight"
	StageCeph      Stage = "ceph"
	StageK8s       Stage = "kubernetes"
	StageCSI       Stage = "csi"
	StageAddons    Stage = "addons"
	StageCompleted Stage = "completed"
	StageFailed    Stage = "failed"
)

// VerifyCheck is one row in the dev-vm verify stage's per-check result.
// Persisted on Run.VerifyResults so Step 6/7 can render PASS/FAIL with
// the exact command output that decided each result.
type VerifyCheck struct {
	ID     string `json:"id"`     // ssh_os_release | hostname_ip_mac | network_dns | package_manager
	Label  string `json:"label"`  // human-readable label
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

type Run struct {
	ID               string              `json:"id"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	Inventory        inventory.Inventory `json:"inventory"`
	Stage            Stage               `json:"stage"`
	LastError        string              `json:"last_error,omitempty"`
	History          []Event             `json:"history"`
	RootPasswordHash      string              `json:"root_password_hash,omitempty"` // SHA-512 crypt; injected into seeds
	HostAdvertiseIP       string              `json:"host_advertise_ip,omitempty"`  // Windows NIC chosen for HTTP base URL
	HTTPBaseURL           string              `json:"http_base_url,omitempty"`      // populated once server binds
	RKE2Token             string              `json:"rke2_token,omitempty"`         // generated once per run, kept for cluster joins
	K3sToken              string              `json:"k3s_token,omitempty"`
	CephDashboardPassword string              `json:"ceph_dashboard_password,omitempty"`

	// VerifyResults is populated by the dev-vm verify stage after SSH
	// reachability. Empty for cluster-mode runs.
	VerifyResults []VerifyCheck `json:"verify_results,omitempty"`
}

type Event struct {
	At    time.Time `json:"at"`
	Stage Stage     `json:"stage"`
	Msg   string    `json:"msg"`
}

type RunSummary struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Target    string    `json:"target"`
	Stage     Stage     `json:"stage"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	mu sync.Mutex
}

func New() *Store { return &Store{} }

func (s *Store) NewRun(inv inventory.Inventory) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	r := Run{
		ID:        id,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Inventory: inv,
		Stage:     StagePending,
	}
	if err := s.save(r); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) Load(id string) (Run, error) {
	p := filepath.Join(runtime.RunsDir(), id, "run.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(raw, &r); err != nil {
		return Run{}, err
	}
	return r, nil
}

func (s *Store) save(r Run) error {
	dir := filepath.Join(runtime.RunsDir(), r.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r.UpdatedAt = time.Now()
	tmp := filepath.Join(dir, "run.json.tmp")
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "run.json"))
}

// Update applies fn under lock and persists the result.
func (s *Store) Update(id string, fn func(*Run)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.Load(id)
	if err != nil {
		return err
	}
	fn(&r)
	return s.save(r)
}

// List enumerates recent runs (oldest-first eviction left to caller).
func (s *Store) List() ([]RunSummary, error) {
	entries, err := os.ReadDir(runtime.RunsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RunSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, RunSummary{
			ID:        r.ID,
			Cluster:   r.Inventory.Cluster.Name,
			Target:    fmt.Sprintf("%s://%s", r.Inventory.Target.Type, r.Inventory.Target.Endpoint),
			Stage:     r.Stage,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// OpenRunCount is used by the shutdown hook for a status line.
func (s *Store) OpenRunCount() int {
	rs, _ := s.List()
	n := 0
	for _, r := range rs {
		if r.Stage != StageCompleted && r.Stage != StageFailed {
			n++
		}
	}
	return n
}
