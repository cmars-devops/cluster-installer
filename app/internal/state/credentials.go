// Saved node-credential registry. Mirrors the saved-target registry
// (state/targets.go) but for the cluster_auth bag the operator sets up
// in Step 1 — sudo username, SSH key sources, and the optional console
// password. Persisted at %LOCALAPPDATA%\cluster-installer\credentials.json
// so the same SSH-key set / sudo username can be picked from a dropdown
// across runs instead of retyping every time.
//
// Why split from servers.json? Different lifecycles. A hypervisor
// endpoint pairs naturally with a credential set in some shops (one
// lab, one set of keys) but in others a single GitHub-imported key set
// is used across many hypervisors. Keeping the registries separate
// lets the operator mix-and-match: pick saved hypervisor #2 in Step 2,
// pick saved credential set "lab keys" in Step 1.
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

// SavedCredential captures the cluster_auth subtree the wizard's Step 1
// "노드 인증 자격 증명" section produces. Field names match
// inventory.ClusterAuthSpec to make population trivial.
type SavedCredential struct {
	ID    string `json:"id"`
	Label string `json:"label"`

	// Username is the sudo account autoinstall creates (defaults to
	// "triangles" downstream when blank). Empty in the registry means
	// "use the wizard default" — same convention as run state.
	Username string `json:"username,omitempty"`

	// SSHImportGitHub is the list of GitHub usernames whose .keys are
	// fetched at first boot via ssh-import-id-gh.
	SSHImportGitHub []string `json:"ssh_import_github,omitempty"`

	// SSHAuthorizedKeys is the raw-paste fallback (offline / non-GitHub).
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys,omitempty"`

	// NodePassword is the optional console password. Plaintext, same
	// trust boundary as run.json — file lives under user profile ACL.
	NodePassword string `json:"node_password,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// credsFile is %LOCALAPPDATA%\cluster-installer\credentials.json.
func credsFile() string {
	return filepath.Join(runtime.AppDataDir(), "credentials.json")
}

type credsDoc struct {
	Credentials []SavedCredential `json:"credentials"`
}

// CredentialStore is concurrency-safe (mutex + atomic temp-rename),
// matching the TargetStore pattern.
type CredentialStore struct {
	mu sync.Mutex
}

func NewCredentialStore() *CredentialStore { return &CredentialStore{} }

func (s *CredentialStore) readUnlocked() (credsDoc, error) {
	raw, err := os.ReadFile(credsFile())
	if err != nil {
		if os.IsNotExist(err) {
			return credsDoc{Credentials: []SavedCredential{}}, nil
		}
		return credsDoc{}, err
	}
	var doc credsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return credsDoc{}, fmt.Errorf("parse credentials.json: %w", err)
	}
	if doc.Credentials == nil {
		doc.Credentials = []SavedCredential{}
	}
	return doc, nil
}

func (s *CredentialStore) writeUnlocked(doc credsDoc) error {
	if err := os.MkdirAll(runtime.AppDataDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := credsFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, credsFile())
}

// List returns saved credentials sorted by LastUsedAt descending —
// most-recently-used floats to the top of the dropdown.
func (s *CredentialStore) List() ([]SavedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return nil, err
	}
	out := make([]SavedCredential, len(doc.Credentials))
	copy(out, doc.Credentials)
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUsedAt.After(out[j].LastUsedAt)
	})
	return out, nil
}

// Save upserts a credential. Empty ID = new entry; set CreatedAt + UUID.
// Returns the stored record so the frontend has the canonical timestamps.
func (s *CredentialStore) Save(c SavedCredential) (SavedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return SavedCredential{}, err
	}
	now := time.Now()
	if c.ID == "" {
		c.ID = uuid.NewString()
		c.CreatedAt = now
	}
	c.LastUsedAt = now

	if c.Label == "" {
		switch {
		case len(c.SSHImportGitHub) > 0:
			c.Label = "GitHub: " + c.SSHImportGitHub[0]
			if len(c.SSHImportGitHub) > 1 {
				c.Label += fmt.Sprintf(" (+%d)", len(c.SSHImportGitHub)-1)
			}
		case c.Username != "":
			c.Label = "sudo: " + c.Username
		default:
			c.Label = "credentials"
		}
	}

	found := false
	for i := range doc.Credentials {
		if doc.Credentials[i].ID == c.ID {
			c.CreatedAt = doc.Credentials[i].CreatedAt
			doc.Credentials[i] = c
			found = true
			break
		}
	}
	if !found {
		doc.Credentials = append(doc.Credentials, c)
	}
	if err := s.writeUnlocked(doc); err != nil {
		return SavedCredential{}, err
	}
	return c, nil
}

// Delete removes by ID. Missing ID is not an error.
func (s *CredentialStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	out := doc.Credentials[:0]
	for _, c := range doc.Credentials {
		if c.ID != id {
			out = append(out, c)
		}
	}
	doc.Credentials = out
	return s.writeUnlocked(doc)
}

// Touch bumps LastUsedAt without changing other fields — called when
// the operator picks a saved row.
func (s *CredentialStore) Touch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readUnlocked()
	if err != nil {
		return err
	}
	for i := range doc.Credentials {
		if doc.Credentials[i].ID == id {
			doc.Credentials[i].LastUsedAt = time.Now()
			return s.writeUnlocked(doc)
		}
	}
	return nil
}
