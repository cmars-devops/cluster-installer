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
	// VM disk datastore is per-node (Step 4); ISO upload datastore is
	// usually the stack-level Step 2 choice. dev-vm mode lets a single
	// node datastore cover both — picking once for one VM is reasonable.
	isoDS := t.ISODatastore
	if isoDS == "" {
		isoDS = t.Datastore
	}
	if isoDS == "" && o.Inventory.Cluster.IsDevVM() && len(o.Inventory.Nodes) > 0 {
		isoDS = o.Inventory.Nodes[0].Datastore
	}
	if isoDS == "" {
		return fmt.Errorf("ISO upload datastore is required (Step 4 → \"VM 디스크 데이터스토어\" or Step 2)")
	}

	c, err := esxi.NewClient(ctx, t)
	if err != nil {
		return fmt.Errorf("connect ESXi: %w", err)
	}
	defer c.Close(ctx)

	o.emit("run:line", fmt.Sprintf("→ uploading per-node ISOs to [%s]", isoDS))
	emit := func(line string) { o.emit("run:line", line) }

	dsPrefix := "cluster-installer/" + o.Run.ID + "/"

	// Track which OS-shared install ISOs have already been remastered +
	// uploaded for this run, so a 9-OSD-node Ubuntu cluster pays the
	// 3 GB upload cost ONCE instead of nine times. Keyed by OS family.
	sharedUploaded := make(map[string]bool)

	for _, n := range o.Inventory.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 1. Per-node seed CD-ROM. Always present (every node):
		//      MicroOS  → Combustion+Ignition (the install payload itself)
		//      Agama    → secondary CD with first-boot script + SSH keys
		//      Ubuntu   → cidata CD-ROM with user-data + meta-data
		seedLocal := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
		seedRel := dsPrefix + "seed-" + n.Hostname + ".iso"
		if err := c.UploadFile(ctx, isoDS, seedRel, seedLocal, emit); err != nil {
			return fmt.Errorf("upload seed for %s: %w", n.Hostname, err)
		}

		// 2. Per-node Agama-remastered install ISO — Leap/Tumbleweed only.
		// The grub cmdline embeds the per-node HTTP profile URL, so this
		// genuinely cannot be shared across nodes (yet).
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

		// 3. Shared Ubuntu install ISO — remastered + uploaded ONCE per run.
		// All Ubuntu nodes attach the same datastore file. Per-node identity
		// is delivered via the per-node cidata CD-ROM (step 1).
		if n.OS == "ubuntu" && !sharedUploaded["ubuntu"] {
			localISO, err := o.remasterUbuntuShared(ctx)
			if err != nil {
				return fmt.Errorf("ubuntu shared remaster: %w", err)
			}
			installRel := dsPrefix + "install-ubuntu.iso"
			if err := c.UploadFile(ctx, isoDS, installRel, localISO, emit); err != nil {
				return fmt.Errorf("upload shared ubuntu install iso: %w", err)
			}
			sharedUploaded["ubuntu"] = true
			o.emit("run:line", "→ ubuntu install ISO uploaded once and shared by all Ubuntu nodes")
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

// remasterUbuntuShared produces a SINGLE Ubuntu live-server ISO for all
// Ubuntu nodes in this run. The grub cmdline carries the cidata-only form
// `autoinstall ds=nocloud` (no URL) — per-node user-data is delivered via
// a small per-node cidata CD-ROM that cloud-init's NoCloud datasource
// auto-discovers by volume label. That collapses what used to be N × 3 GB
// remasters/uploads into a single one.
func (o *Orchestrator) remasterUbuntuShared(ctx context.Context) (string, error) {
	cat, err := imagecache.LoadCatalog(o.ContentDir)
	if err != nil {
		return "", err
	}
	catKey, img, ok := cat.LookupForOS("ubuntu", "")
	if !ok {
		return "", fmt.Errorf("images.yaml has no ubuntu entry")
	}

	progress := imagecache.Progress(func(line string) { o.emit("run:line", line) })
	cached, err := imagecache.EnsureImage(ctx, apruntime.ImageCache(), catKey, img, progress)
	if err != nil {
		return "", fmt.Errorf("ensure ubuntu iso: %w", err)
	}

	dst := filepath.Join(o.stagingDir, "seeds", "install-ubuntu.iso")
	// Empty URL → remaster.py adds only `autoinstall ds=nocloud` to the
	// kernel cmdline; user-data is delivered via the cidata CD-ROM.
	if err := imagecache.RemasterUbuntuISO(ctx, cached.Path, dst, "", progress); err != nil {
		return "", err
	}
	return dst, nil
}
