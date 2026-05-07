// Package imagecache owns the OS-image side of the run lifecycle:
// it parses content/images.yaml, downloads + sha256-verifies each
// referenced ISO/qcow2 into %LOCALAPPDATA%\cluster-installer\cache\images\,
// and extracts kernel-boot artefacts (vmlinuz, initrd, squashfs) for
// Agama-installed nodes before the orchestrator's HTTP server picks them
// up.
//
// Why a cache: the same content tag can be reused across many runs, and
// re-downloading 1.5 GB per run is unfriendly. Cache key is the upstream
// sha256 — content-addressed, so simultaneous content tags pointing at
// the same upstream ISO share storage automatically.
package imagecache

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Catalog is the deserialised content/images.yaml.
type Catalog struct {
	SchemaVersion int              `yaml:"schema_version"`
	Images        map[string]Image `yaml:"images"`
}

// Image describes one downloadable OS image. Either an ISO (Agama
// kernel-boot family) or a qcow2 (MicroOS self-install family).
//
// SeedFormat tells the orchestrator which seed builder to wire — agama
// (HTTP profile + direct kernel boot) or ignition (Combustion CD-ROM).
type Image struct {
	Family       string `yaml:"family"`        // microos | leap | tumbleweed
	Version      string `yaml:"version"`       // optional, e.g. "15.6"
	Arch         string `yaml:"arch"`          // x86_64
	Type         string `yaml:"type"`          // self-install | agama-live
	URL          string `yaml:"url"`           // HTTP(S) source
	ChecksumURL  string `yaml:"checksum_url"`  // upstream .sha256 file
	SeedFormat   string `yaml:"seed_format"`   // agama | ignition
}

// LoadCatalog reads content/images.yaml from the given content dir.
func LoadCatalog(contentDir string) (Catalog, error) {
	path := filepath.Join(contentDir, "images.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("read images.yaml: %w", err)
	}
	var c Catalog
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Catalog{}, fmt.Errorf("parse images.yaml: %w", err)
	}
	if c.SchemaVersion != 1 {
		return Catalog{}, fmt.Errorf("images.yaml schema_version=%d (expected 1)", c.SchemaVersion)
	}
	return c, nil
}

// LookupForOS returns the catalog entry that matches the node's OS family.
// Multiple images may share a family (e.g. leap-15.6 vs leap-16.0); the
// caller can pin via the optional version string. Empty version picks the
// first-defined entry — order in images.yaml is the implicit default.
func (c Catalog) LookupForOS(family, version string) (string, Image, bool) {
	for key, img := range c.Images {
		if img.Family != family {
			continue
		}
		if version != "" && img.Version != version {
			continue
		}
		return key, img, true
	}
	return "", Image{}, false
}

// NeedsKernelBoot reports whether the orchestrator must extract
// vmlinuz/initrd/squashfs from this image (Agama direct kernel boot).
// MicroOS self-install qcow2 doesn't need extraction — the image IS the
// boot disk.
func (i Image) NeedsKernelBoot() bool {
	return i.SeedFormat == "agama" && i.Type == "agama-live"
}
