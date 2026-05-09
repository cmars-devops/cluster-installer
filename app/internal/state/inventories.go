// Saved-inventory registry. Lets the operator persist a fully-edited
// cluster topology (cluster + network + nodes + ceph + addons +
// content) and recall it from a dropdown on Step 4 instead of
// retyping a 9-node Ceph layout from memory every run.
//
// Why a third store (next to targets.go + credentials.go)?
//   - Lifecycle: an inventory describes WHAT to deploy. Targets
//     describe WHERE to deploy it. Credentials describe HOW to log
//     in once it's deployed. Operators rotate them independently —
//     same topology against multiple hypervisors during testing,
//     same hypervisor with different topologies during dev. Forcing
//     a one-to-one bundle would defeat that.
//   - We deliberately exclude TargetSpec + ClusterAuthSpec from the
//     saved payload so 'load inventory' never overwrites whichever
//     hypervisor / SSH-key set the operator already picked.
//   - PrimaryMAC + per-NIC MAC values are stripped on save: those
//     are run-time-allocated and would conflict with a redeploy
//     under a different cluster name.
//
// Storage: %LOCALAPPDATA%\cluster-installer\inventories.json,
// atomic temp-rename, same trust boundary as the other registries.
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

	"github.com/cmars-devops/cluster-installer/internal/inventory"
	"github.com/cmars-devops/cluster-installer/internal/runtime"
)

// SavedInventory is the persisted cluster topology — all the bits an
// operator iterates on between runs, none of the bits that depend on
// the specific test environment (no target, no credentials).
type SavedInventory struct {
	ID    string `json:"id"`
	Label string `json:"label"`

	// Cluster topology. Mirror inventory.* names so populating
	// from a wizard state is a direct field copy.
	Cluster inventory.ClusterSpec `json:"cluster"`
	Network inventory.NetworkSpec `json:"network"`
	Nodes   []inventory.NodeSpec  `json:"nodes"`
	Ceph    inventory.CephSpec    `json:"ceph"`
	Addons  inventory.AddonsSpec  `json:"addons"`
	Content inventory.ContentSpec `json:"content"`

	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

func invsFile() string {
	return filepath.Join(runtime.AppDataDir(), "inventories.json")
}

type invsDoc struct {
	Inventories []SavedInventory `json:"inventories"`
}

// InventoryStore — concurrency-safe (mutex + atomic temp-rename),
// matches the TargetStore / CredentialStore pattern.
type InventoryStore struct {
	mu sync.Mutex
}

func NewInventoryStore() *InventoryStore { return &InventoryStore{} }

func (s *InventoryStore) readUnlocked() (invsDoc, error) {
	raw, err := os.ReadFile(invsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return invsDoc{Inventories: []SavedInventory{}}, nil
		}
		return invsDoc{}, err
	}
	var doc invsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return invsDoc{}, fmt.Errorf("parse inventories.json: %w", err)
	}
	if doc.Inventories == nil {
		doc.Inventories = []SavedInventory{}
	}
	return doc, nil
}

func (s *InventoryStore) writeUnlocked(doc invsDoc) error {
	if err := os.MkdirAll(runtime.AppDataDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := invsFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, invsFile())
}

// List returns saved inventories sorted by LastUsedAt descending.
func (s *InventoryStore) List() ([]SavedInventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return nil, err
	}
	out := make([]SavedInventory, len(doc.Inventories))
	copy(out, doc.Inventories)
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUsedAt.After(out[j].LastUsedAt)
	})
	return out, nil
}

// Save upserts a saved inventory. Empty ID = new entry. Run-time
// volatile fields (PrimaryMAC, per-NIC MACs) are stripped before
// persisting so a redeploy under a different cluster name doesn't
// inherit stale MAC bindings.
func (s *InventoryStore) Save(inv SavedInventory) (SavedInventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return SavedInventory{}, err
	}
	now := time.Now()
	if inv.ID == "" {
		inv.ID = uuid.NewString()
		inv.CreatedAt = now
	}
	inv.LastUsedAt = now

	// Strip run-time-allocated fields. Caller may have copied them
	// from a live wizard state where ensureNodeMACs() already ran.
	for i := range inv.Nodes {
		inv.Nodes[i].PrimaryMAC = ""
		for j := range inv.Nodes[i].NICs {
			inv.Nodes[i].NICs[j].MAC = ""
		}
	}

	// Auto-label so the dropdown is never blank. Operators can
	// rename via prompt on save; this is just the default.
	if inv.Label == "" {
		topo := inv.Cluster.Topology
		if topo == "" {
			topo = "combined"
		}
		nodeCount := len(inv.Nodes)
		switch {
		case inv.Cluster.Name != "":
			inv.Label = fmt.Sprintf("%s (%d nodes, %s)", inv.Cluster.Name, nodeCount, topo)
		default:
			inv.Label = fmt.Sprintf("%d nodes / %s", nodeCount, topo)
		}
	}

	found := false
	for i := range doc.Inventories {
		if doc.Inventories[i].ID == inv.ID {
			inv.CreatedAt = doc.Inventories[i].CreatedAt
			doc.Inventories[i] = inv
			found = true
			break
		}
	}
	if !found {
		doc.Inventories = append(doc.Inventories, inv)
	}
	if err := s.writeUnlocked(doc); err != nil {
		return SavedInventory{}, err
	}
	return inv, nil
}

// Delete removes by ID. Missing ID is not an error.
func (s *InventoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	out := doc.Inventories[:0]
	for _, t := range doc.Inventories {
		if t.ID != id {
			out = append(out, t)
		}
	}
	doc.Inventories = out
	return s.writeUnlocked(doc)
}

// Touch bumps LastUsedAt without other mutations.
func (s *InventoryStore) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	for i := range doc.Inventories {
		if doc.Inventories[i].ID == id {
			doc.Inventories[i].LastUsedAt = time.Now()
			return s.writeUnlocked(doc)
		}
	}
	return nil
}
