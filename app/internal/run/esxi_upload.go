// ESXi pre-TF stage: push every node's CD-ROM media to the vSphere
// datastore at the path tfvars.go references. The orchestrator runs
// this only for target.type=esxi; libvirt and Proxmox can read the
// host filesystem directly.
//
// Two ISO flavours land on the datastore per run:
//
//	seed-<host>.iso       Combustion+Ignition seed (always — every node
//	                      gets one even Agama nodes, used as a secondary
//	                      CD-ROM that carries SSH keys / hostname / etc.
//	                      on first boot for Leap/Tumbleweed too).
//	install-<host>.iso    Per-node Agama-remastered netinstall image —
//	                      only for Leap/Tumbleweed. The kernel cmdline
//	                      inside the ISO is rewritten to point at our
//	                      HTTP-served auto-install profile.
package run

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cmars-devops/cluster-installer/internal/imagecache"
	"github.com/cmars-devops/cluster-installer/internal/runner/esxi"
	apruntime "github.com/cmars-devops/cluster-installer/internal/runtime"
)

func (o *Orchestrator) uploadSeedsToDatastore(ctx context.Context) error {
	t := o.Inventory.Target
	if t.Datastore == "" {
		return fmt.Errorf("target.datastore is required for ESXi runs (Step 2 → datastore picker)")
	}
	isoDS := t.ISODatastore
	if isoDS == "" {
		isoDS = t.Datastore
	}

	c, err := esxi.NewClient(ctx, t)
	if err != nil {
		return fmt.Errorf("connect ESXi: %w", err)
	}
	defer c.Close(ctx)

	o.emit("run:line", fmt.Sprintf("→ uploading per-node ISOs to [%s]", isoDS))
	emit := func(line string) { o.emit("run:line", line) }

	dsPrefix := "cluster-installer/" + o.Run.ID + "/"

	for _, n := range o.Inventory.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 1. Combustion seed always — secondary CD-ROM for Agama nodes,
		//    primary boot config for MicroOS (which has no remastered ISO).
		seedLocal := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
		seedRel := dsPrefix + "seed-" + n.Hostname + ".iso"
		if err := c.UploadFile(ctx, isoDS, seedRel, seedLocal, emit); err != nil {
			return fmt.Errorf("upload seed for %s: %w", n.Hostname, err)
		}

		// 2. Per-node Agama-remastered install ISO — Leap/Tumbleweed only.
		if n.OS == "leap" || n.OS == "tumbleweed" {
			localISO, err := o.remasterAgamaForNode(ctx, n.Hostname, n.OS)
			if err != nil {
				return fmt.Errorf("remaster %s: %w", n.Hostname, err)
			}
			installRel := dsPrefix + "install-" + n.Hostname + ".iso"
			if err := c.UploadFile(ctx, isoDS, installRel, localISO, emit); err != nil {
				return fmt.Errorf("upload install ISO for %s: %w", n.Hostname, err)
			}
		}
	}
	o.emit("run:line", "→ all per-node ISOs uploaded")
	return nil
}

// remasterAgamaForNode produces a per-node netinstall ISO whose grub /
// isolinux entries contain `inst.auto=http://<host>/profiles/<name>.json`.
// Caches by (sha256-of-source-iso, hostname) so re-runs of the same
// content tag don't pay the I/O cost twice — the rewrite is purely a
// function of the source ISO + the per-node profile URL, both of which
// are stable inputs.
func (o *Orchestrator) remasterAgamaForNode(ctx context.Context, hostname, osFamily string) (string, error) {
	cat, err := imagecache.LoadCatalog(o.ContentDir)
	if err != nil {
		return "", err
	}
	catKey, img, ok := cat.LookupForOS(osFamily, "")
	if !ok {
		return "", fmt.Errorf("images.yaml has no entry for family=%s", osFamily)
	}

	progress := imagecache.Progress(func(line string) { o.emit("run:line", line) })
	cached, err := imagecache.EnsureImage(ctx, apruntime.ImageCache(), catKey, img, progress)
	if err != nil {
		return "", fmt.Errorf("ensure source iso: %w", err)
	}

	dst := filepath.Join(o.stagingDir, "seeds", "install-"+hostname+".iso")
	profileURL := o.baseURL + "/profiles/" + hostname + ".json"
	installURL := o.baseURL + "/repo"

	if err := imagecache.RemasterAgamaISO(ctx, cached.Path, dst, profileURL, installURL, progress); err != nil {
		return "", err
	}
	return dst, nil
}
