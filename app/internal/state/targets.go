// Saved-target registry. Lets the operator pick from previously-used
// hypervisor endpoints (ESXi/libvirt/Proxmox) on Step 2 instead of
// retyping them every run. Persisted as a single JSON file at
// %LOCALAPPDATA%\cluster-installer\servers.json.
//
// Why a flat JSON file (not per-target dir like runs)? Two reasons:
//   1. The list is small (operators typically manage <20 hypervisors)
//      so a single read on Step 2 mount is cheap.
//   2. Add/delete operations need to touch the whole list anyway —
//      atomic temp-file rename gives crash-safety in one shot.
//
// Security: passwords are stored plaintext, same as run.json. The
// file lives under the user profile (Windows ACL: only the owner can
// read), so the threat model is no worse than the existing run state.
// We do NOT export this file or sync it to a repo — the registry is
// purely a local convenience.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/cmars-devops/cluster-installer/internal/runtime"
)

// SavedTarget mirrors inventory.TargetSpec but adds an ID + label so
// the operator can give a recognisable name (e.g. "ESXi DEV (lab)") and
// pick by it on Step 2. Fields outside TargetSpec stay null when not
// applicable (e.g. APIToken is libvirt-empty).
type SavedTarget struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"` // libvirt | proxmox | esxi
	Endpoint    string `json:"endpoint"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`     // ESXi (vSphere API + SSH share)
	SSHKey      string `json:"ssh_key,omitempty"`      // libvirt (required), ESXi (optional alt)
	APIToken    string `json:"api_token,omitempty"`    // proxmox
	TLSInsecure bool   `json:"tls_insecure,omitempty"` // ESXi/proxmox lab default = true

	// ESXi-specific placement defaults — populated when the operator
	// commits the saved target after a successful Discover so future
	// runs don't have to re-pick the same datastore/network. Empty for
	// libvirt/Proxmox or when the operator chose not to capture them.
	Datastore    string `json:"datastore,omitempty"`
	ISODatastore string `json:"iso_datastore,omitempty"`
	Network      string `json:"network,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// targetsFile is %LOCALAPPDATA%\cluster-installer\servers.json.
func targetsFile() string {
	return filepath.Join(runtime.AppDataDir(), "servers.json")
}

// targetsDoc is the on-disk shape. Wrapped so we can add metadata
// (schema version, last-saved-at) later without breaking parsing.
type targetsDoc struct {
	Targets []SavedTarget `json:"targets"`
}

// TargetStore is concurrency-safe — operations grab the mutex,
// re-read the file, mutate, and atomic-rename back out. Keeps multiple
// Wails frontend windows in sync without a daemon.
type TargetStore struct {
	mu sync.Mutex
}

func NewTargetStore() *TargetStore { return &TargetStore{} }

// readUnlocked reads the doc without taking the lock. Caller holds it.
func (s *TargetStore) readUnlocked() (targetsDoc, error) {
	raw, err := os.ReadFile(targetsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return targetsDoc{Targets: []SavedTarget{}}, nil
		}
		return targetsDoc{}, err
	}
	var doc targetsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return targetsDoc{}, fmt.Errorf("parse servers.json: %w", err)
	}
	if doc.Targets == nil {
		doc.Targets = []SavedTarget{}
	}
	return doc, nil
}

// writeUnlocked atomic-renames a temp file over the doc. Caller holds.
func (s *TargetStore) writeUnlocked(doc targetsDoc) error {
	if err := os.MkdirAll(runtime.AppDataDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := targetsFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, targetsFile())
}

// List returns saved targets sorted by LastUsedAt descending — the
// most recently picked hypervisor floats to the top of the dropdown.
func (s *TargetStore) List() ([]SavedTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return nil, err
	}
	out := make([]SavedTarget, len(doc.Targets))
	copy(out, doc.Targets)
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUsedAt.After(out[j].LastUsedAt)
	})
	return out, nil
}

// Save upserts a target. Empty ID → generate UUID + set CreatedAt.
// Existing ID → merge fields (caller-provided wins) and bump
// LastUsedAt. Returns the saved record so the frontend has the
// canonical ID + timestamps.
func (s *TargetStore) Save(t SavedTarget) (SavedTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return SavedTarget{}, err
	}
	now := time.Now()
	if t.ID == "" {
		t.ID = uuid.NewString()
		t.CreatedAt = now
	}
	t.LastUsedAt = now

	// Auto-label so the dropdown is never blank. Operators can rename
	// later; we just want SOMETHING readable until they do.
	if t.Label == "" {
		if t.Endpoint != "" {
			t.Label = t.Type + " · " + t.Endpoint
		} else {
			t.Label = t.Type
		}
	}

	found := false
	for i := range doc.Targets {
		if doc.Targets[i].ID == t.ID {
			// Preserve CreatedAt across updates.
			t.CreatedAt = doc.Targets[i].CreatedAt
			doc.Targets[i] = t
			found = true
			break
		}
	}
	if !found {
		doc.Targets = append(doc.Targets, t)
	}
	if err := s.writeUnlocked(doc); err != nil {
		return SavedTarget{}, err
	}
	return t, nil
}

// Delete removes by ID. Missing ID is not an error — UI may double-fire.
func (s *TargetStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	out := doc.Targets[:0]
	for _, t := range doc.Targets {
		if t.ID != id {
			out = append(out, t)
		}
	}
	doc.Targets = out
	return s.writeUnlocked(doc)
}

// Touch bumps LastUsedAt on a saved target — called when the operator
// picks one from the dropdown so the list re-sorts on next render.
// No-op when the ID is gone (deleted between list and pick).
func (s *TargetStore) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	for i := range doc.Targets {
		if doc.Targets[i].ID == id {
			doc.Targets[i].LastUsedAt = time.Now()
			return s.writeUnlocked(doc)
		}
	}
	return nil
}
