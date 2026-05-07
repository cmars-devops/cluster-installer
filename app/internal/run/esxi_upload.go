// ESXi pre-TF stage: push every node's seed ISO to the vSphere datastore
// at the path tfvars.go references. The orchestrator runs this only for
// target.type=esxi; libvirt and Proxmox seeds attach directly from the
// local staging dir (their providers can read the host filesystem).
package run

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cmars-devops/cluster-installer/internal/runner/esxi"
)

// uploadSeedsToDatastore fans the local staging/seeds/seed-<host>.iso
// files out to the chosen iso datastore at the path the tfvars renderer
// expects. Each upload streams via govmomi's HTTP /folder endpoint with
// 2-second progress lines re-emitted as run:line events.
//
// Pre-flight check: ensure target.Datastore + target.ISODatastore are
// set — without them tfvars renders empty values and the vSphere
// provider returns an opaque error 30 seconds into apply. Failing fast
// here makes the user message actionable.
func (o *Orchestrator) uploadSeedsToDatastore(ctx context.Context) error {
	t := o.Inventory.Target
	if t.Datastore == "" {
		return fmt.Errorf("target.datastore is required for ESXi runs (Step 2 → datastore picker)")
	}
	isoDS := t.ISODatastore
	if isoDS == "" {
		isoDS = t.Datastore // operator allowed to share
	}

	// Reject Leap/Tumbleweed early on ESXi — Agama profile delivery on
	// vSphere needs ISO remaster (phase-1 §4) and that's not yet shipped.
	// Without remaster the netinstall ISO drops the user into Agama's
	// interactive UI and the run hangs forever waiting for SSH.
	for _, n := range o.Inventory.Nodes {
		if n.OS == "leap" || n.OS == "tumbleweed" {
			return fmt.Errorf("ESXi + Agama (%s) is not yet supported — only MicroOS works on ESXi today. "+
				"Track: docs/phase-1-open-items.md §4 (Agama ISO remaster). "+
				"Workaround: switch %s to microos in Step 3, or pick libvirt as the target.",
				n.OS, n.Hostname)
		}
	}

	c, err := esxi.NewClient(ctx, t)
	if err != nil {
		return fmt.Errorf("connect ESXi: %w", err)
	}
	defer c.Close(ctx)

	o.emit("run:line", fmt.Sprintf("→ uploading %d seed ISOs to [%s]", len(o.Inventory.Nodes), isoDS))
	emit := func(line string) { o.emit("run:line", line) }

	dsPrefix := "cluster-installer/" + o.Run.ID + "/"
	for _, n := range o.Inventory.Nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		local := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
		dsRel := dsPrefix + "seed-" + n.Hostname + ".iso"
		if err := c.UploadFile(ctx, isoDS, dsRel, local, emit); err != nil {
			return fmt.Errorf("upload seed for %s: %w", n.Hostname, err)
		}
	}
	o.emit("run:line", "→ all seed ISOs uploaded")
	return nil
}
