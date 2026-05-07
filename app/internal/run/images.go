// Image staging glue: pulls Agama netinstall ISOs through imagecache and
// drops vmlinuz/initrd/squashfs.img into the run's staging/repo/ tree so
// the embedded HTTP server can serve them at the URLs tfvars.go bakes
// into the libvirt domain's <kernel>/<initrd>/<cmdline> elements.
package run

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cmars-devops/cluster-installer/internal/imagecache"
	apruntime "github.com/cmars-devops/cluster-installer/internal/runtime"
)

// ensureAgamaArtefacts fetches + extracts a netinstall ISO for each
// distinct (family, version) combination present in the inventory.
// MicroOS nodes are skipped entirely — they boot from the qcow2 base
// volume directly via Combustion, no kernel-boot extraction needed.
//
// Idempotent: imagecache short-circuits when the cache already has the
// ISO; ExtractAgamaBoot short-circuits when the staging files are newer
// than the source ISO. Re-runs of the same content tag pay no I/O cost.
func (o *Orchestrator) ensureAgamaArtefacts(ctx context.Context) error {
	if !o.hasAgamaNode() {
		o.emit("run:line", "→ skipping image cache: no Leap/Tumbleweed node in inventory")
		return nil
	}

	cat, err := imagecache.LoadCatalog(o.ContentDir)
	if err != nil {
		return err
	}

	progress := imagecache.Progress(func(line string) { o.emit("run:line", line) })

	// Deduplicate by (family, version) so two Leap nodes don't fetch the
	// same ISO twice.
	seen := make(map[string]bool)
	for _, n := range o.Inventory.Nodes {
		if n.OS != "leap" && n.OS != "tumbleweed" {
			continue
		}
		key := n.OS + "/" + "" // version selector is currently catalog-default
		if seen[key] {
			continue
		}
		seen[key] = true

		catKey, img, ok := cat.LookupForOS(n.OS, "")
		if !ok {
			return fmt.Errorf("images.yaml has no entry for family=%s", n.OS)
		}
		if !img.NeedsKernelBoot() {
			continue
		}

		o.emit("run:line", fmt.Sprintf("→ fetching %s (%s)", catKey, img.URL))
		cached, err := imagecache.EnsureImage(ctx, apruntime.ImageCache(), catKey, img, progress)
		if err != nil {
			return fmt.Errorf("ensure image %s: %w", catKey, err)
		}

		repoDir := filepath.Join(o.stagingDir, "repo")
		if _, err := imagecache.ExtractAgamaBoot(ctx, cached.Path, repoDir, progress); err != nil {
			return fmt.Errorf("extract %s: %w", catKey, err)
		}
	}
	return nil
}

// hasAgamaNode is a quick gate: the orchestrator only pays the image-cache
// cost if at least one node will go through the Agama install path.
func (o *Orchestrator) hasAgamaNode() bool {
	for _, n := range o.Inventory.Nodes {
		if n.OS == "leap" || n.OS == "tumbleweed" {
			return true
		}
	}
	return false
}
