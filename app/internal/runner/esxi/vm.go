// VM lifecycle helpers — small surface for post-install housekeeping
// the orchestrator does after terraform handed back control:
//   - DetachAllCDROMs   eject the install ISOs and remove the CD/DVD
//                       devices entirely so they don't litter the VM's
//                       hardware list.
//   - DeleteDatastoreDir purge the per-run upload tree from the ISO
//                       datastore. The ISOs are single-use; keeping
//                       them around just consumes datastore space.
package esxi

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// DetachAllCDROMs removes every CD/DVD device from the VM. Because
// ESXi refuses to hot-remove a CD-ROM device on a powered-on VM
// ("현재 상태(전원 켜짐)에서는 시도된 작업을 수행할 수 없습니다"), and
// because the guest OS frequently locks the optical device after boot
// — triggering the vSphere "VM has locked CD-ROM door, ignore?"
// question modal — we follow the only path that reliably succeeds:
//
//	1. Answer any pending question (auto-Yes) so a previously-stuck
//	   detach attempt doesn't keep us from acting.
//	2. Graceful shutdown via VMware tools (open-vm-tools is up by now
//	   because verify ran ssh-based checks).
//	3. Reconfigure: remove every CD-ROM device.
//	4. Power back on. The VM comes back without the CD/DVD hardware.
//
// Total wall-clock cost: ~30-60 s for the shutdown+reconfigure+boot
// cycle. The user-facing tradeoff is "wait a bit longer for a clean
// VM" vs "leave stale empty CD-ROM hardware until next destroy" —
// after the user-reported field issue with locked optical media, we
// pick the cleaner outcome.
//
// vmName matches the inventory `name` value (NodeSpec.DisplayName ||
// NodeSpec.Hostname). Lookup is via list+name-equality, not the
// path-based finder, because display_name often contains "@".
func (c *Client) DetachAllCDROMs(ctx context.Context, vmName string, emit func(string)) error {
	log := func(format string, a ...any) {
		if emit != nil {
			emit(fmt.Sprintf(format, a...))
		}
	}

	log("eject CD: looking up VM %q", vmName)
	vm, err := c.findVMByName(ctx, vmName)
	if err != nil {
		log("eject CD: %v — skipping", err)
		return nil
	}
	if vm == nil {
		log("eject CD: VM %q not found in inventory — skipping", vmName)
		return nil
	}

	// 1. Clear any pending question — usually the "guest locked the
	//    CD-ROM, ignore?" prompt left over from a previous detach try.
	if err := answerPendingQuestion(ctx, vm, log); err != nil {
		log("eject CD: answer question — %v (continuing)", err)
	}

	devices, err := vm.Device(ctx)
	if err != nil {
		return fmt.Errorf("list devices on %s: %w", vmName, err)
	}
	cdroms := devices.SelectByType((*types.VirtualCdrom)(nil))
	if len(cdroms) == 0 {
		log("eject CD: %s has no CD/DVD devices (already cleaned up)", vmName)
		return nil
	}
	log("eject CD: %s has %d CD/DVD device(s)", vmName, len(cdroms))

	// 2. Power off the VM (graceful shutdown via guest tools, hard
	//    fallback if that times out). Track whether the VM was on
	//    so we can power it back on at the end.
	var props mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"runtime.powerState"}, &props); err != nil {
		return fmt.Errorf("read powerState: %w", err)
	}
	wasOn := props.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn
	if wasOn {
		if err := powerOffForReconfigure(ctx, vm, log); err != nil {
			return fmt.Errorf("power off %s: %w", vmName, err)
		}
	}

	// 3. Remove every CD-ROM device.
	rmSpecs := make([]types.BaseVirtualDeviceConfigSpec, 0, len(cdroms))
	for _, d := range cdroms {
		rmSpecs = append(rmSpecs, &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationRemove,
			Device:    d,
		})
	}
	rmTask, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{DeviceChange: rmSpecs})
	if err != nil {
		// Try to recover: power back on and surface the error.
		if wasOn {
			_, _ = vm.PowerOn(ctx)
		}
		return fmt.Errorf("reconfigure remove %s: %w", vmName, err)
	}
	if err := rmTask.Wait(ctx); err != nil {
		if wasOn {
			_, _ = vm.PowerOn(ctx)
		}
		return fmt.Errorf("wait reconfigure remove %s: %w", vmName, err)
	}
	log("eject CD: removed %d CD/DVD device(s) from %s", len(cdroms), vmName)

	// 4. Power back on if it started on. We don't wait for SSH —
	//    verify already passed; the user just needs the VM running.
	if wasOn {
		log("eject CD: powering %s back on", vmName)
		onTask, err := vm.PowerOn(ctx)
		if err != nil {
			return fmt.Errorf("power on %s: %w", vmName, err)
		}
		if err := onTask.Wait(ctx); err != nil {
			return fmt.Errorf("wait power on %s: %w", vmName, err)
		}
		log("eject CD: %s powered on (sshd will be available in ~20s)", vmName)
	}
	return nil
}

// answerPendingQuestion clears a vSphere "Question" modal stuck on the
// VM (e.g. "Guest has locked CD-ROM, ignore?"). It picks the choice
// whose label/key contains "yes" — which for the CD-lock question is
// the "ignore the lock and proceed" branch we want anyway. No-op when
// no question is pending.
func answerPendingQuestion(ctx context.Context, vm *object.VirtualMachine, log func(string, ...any)) error {
	var props mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"runtime.question"}, &props); err != nil {
		return err
	}
	q := props.Runtime.Question
	if q == nil {
		return nil
	}
	choice := ""
	for _, ci := range q.Choice.ChoiceInfo {
		ed := ci.GetElementDescription()
		if ed == nil {
			continue
		}
		k := strings.ToLower(ed.Key)
		l := strings.ToLower(ed.Label)
		if strings.Contains(k, "yes") || strings.Contains(l, "yes") || strings.Contains(l, "ignore") {
			choice = ed.Key
			break
		}
	}
	if choice == "" && len(q.Choice.ChoiceInfo) > 0 {
		// Fallback: take the first available choice.
		if ed := q.Choice.ChoiceInfo[0].GetElementDescription(); ed != nil {
			choice = ed.Key
		}
	}
	if choice == "" {
		return fmt.Errorf("question %q has no answerable choice", q.Text)
	}
	log("eject CD: answering vSphere question %q with %q", strings.SplitN(q.Text, "\n", 2)[0], choice)
	return vm.Answer(ctx, q.Id, choice)
}

// CDROMSource describes one ISO file currently bound to a CD-ROM device
// on a VM — the source of truth for "where vSphere thinks the install
// media lives". Captured BEFORE we detach CDs so the orchestrator can
// delete THOSE EXACT paths afterwards (instead of reconstructing the
// path from inventory + run-id, which has bitten us in the field when
// search-by-name silently failed).
type CDROMSource struct {
	VMName        string
	DatastoreRef  types.ManagedObjectReference // direct MoRef to the datastore (no name lookup needed)
	DatastoreName string                        // for human logs and SSH-fallback path construction
	FullPath      string                        // "[ds] cluster-installer/<id>/install-ubuntu.iso"
	DsRelPath     string                        // "cluster-installer/<id>/install-ubuntu.iso"
	ParentDir     string                        // "cluster-installer/<id>"
}

// CollectCDROMSources reads the named VM's device list and returns every
// ISO-backed CD-ROM. Empty result for a VM with no ISO-backed CD-ROMs
// (already cleaned up, never had any, etc.) — no error.
func (c *Client) CollectCDROMSources(ctx context.Context, vmName string) ([]CDROMSource, error) {
	vm, err := c.findVMByName(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("find vm %q: %w", vmName, err)
	}
	if vm == nil {
		return nil, nil
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, fmt.Errorf("list devices on %s: %w", vmName, err)
	}
	var out []CDROMSource
	for _, d := range devices.SelectByType((*types.VirtualCdrom)(nil)) {
		cd, ok := d.(*types.VirtualCdrom)
		if !ok {
			continue
		}
		iso, ok := cd.Backing.(*types.VirtualCdromIsoBackingInfo)
		if !ok || iso.FileName == "" {
			continue
		}
		dsName, rel := splitDatastorePath(iso.FileName)
		parent := rel
		if i := strings.LastIndexByte(rel, '/'); i >= 0 {
			parent = rel[:i]
		}
		src := CDROMSource{
			VMName:        vmName,
			DatastoreName: dsName,
			FullPath:      iso.FileName,
			DsRelPath:     rel,
			ParentDir:     parent,
		}
		if iso.Datastore != nil {
			src.DatastoreRef = *iso.Datastore
		}
		out = append(out, src)
	}
	return out, nil
}

// splitDatastorePath turns "[ds] folder/file" into ("ds", "folder/file").
// Returns ("", original) if the input doesn't have the expected shape.
func splitDatastorePath(s string) (name, rel string) {
	if !strings.HasPrefix(s, "[") {
		return "", s
	}
	end := strings.Index(s, "]")
	if end < 0 {
		return "", s
	}
	name = s[1:end]
	rel = strings.TrimSpace(s[end+1:])
	return
}

// DeleteFileAtPath deletes a single file using the literal datastore
// path string ("[ds] path/file") — no name resolution, no Path()
// reconstruction. Used when the path was read from a VM device backing,
// so we know vSphere accepts this exact format.
func (c *Client) DeleteFileAtPath(ctx context.Context, fullDsPath string) error {
	fm := object.NewFileManager(c.gc.Client)
	t, err := fm.DeleteDatastoreFile(ctx, fullDsPath, c.dc)
	if err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	if err := t.Wait(ctx); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}

// TryRemoveEmptyDir attempts to delete a directory on a datastore.
// Succeeds only when the directory is actually empty — vSphere's
// DeleteDatastoreFile_Task documents "If a directory is to be deleted,
// the directory must be empty." Non-empty → returns an error → caller
// treats it as "leave alone, something else lives here".
//
// Used to clean up the `cluster-installer/` parent after wiping every
// run-id we own: if no UNKNOWN children remain, the parent rmdir
// succeeds and the whole tree is gone; if UNKNOWN children DO remain,
// the rmdir fails harmlessly and we leave the parent so the unknown
// data stays untouched.
func (c *Client) TryRemoveEmptyDir(ctx context.Context, datastore, dsRel string) error {
	ds, err := c.finder.Datastore(ctx, datastore)
	if err != nil {
		return fmt.Errorf("datastore: %w", err)
	}
	fm := object.NewFileManager(c.gc.Client)
	t, err := fm.DeleteDatastoreFile(ctx, ds.Path(path.Clean(dsRel)), c.dc)
	if err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	if err := t.Wait(ctx); err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	return nil
}

// LeftoverEntry is one staging dir found under cluster-installer/ on a
// datastore. Path is the run-id (the immediate child name); Files lists
// every regular file within, with size for the operator to gauge waste.
//
// Owned reflects whether this run-id corresponds to a run we have a
// local run.json for (`%LOCALAPPDATA%\cluster-installer\runs\<id>\`).
// We only ever wipe Owned=true entries. Owned=false entries are flagged
// to the operator but left alone — they could belong to a parallel
// installer instance, a different operator, or another tool that
// happens to use the same `cluster-installer/` prefix on this VMFS.
type LeftoverEntry struct {
	Path  string         `json:"path"`           // run-id directory name
	Owned bool           `json:"owned"`          // run-id matches one of OUR local runs
	Files []LeftoverFile `json:"files"`          // ISOs inside (typically seed-*.iso, install-*.iso)
	Error string         `json:"error,omitempty"` // populated on per-dir list failure
}

// LeftoverFile is a single file entry inside a LeftoverEntry.
type LeftoverFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ListClusterInstallerEntries lists every immediate child of
// "cluster-installer/" on the datastore — i.e. one entry per run that
// uploaded ISOs there. Used by the wizard's Step 7 diagnostic panel
// so the operator can see what's piling up before deciding whether to
// wipe.
//
// ourRunIDs is the set of run-ids the wizard has a local run.json
// for. Each returned entry's Owned flag is set to (entry.Path in
// ourRunIDs). Wipe operations only act on Owned=true entries; the
// rest are flagged to the operator and left alone.
func (c *Client) ListClusterInstallerEntries(ctx context.Context, datastore string, ourRunIDs map[string]bool) ([]LeftoverEntry, error) {
	ds, err := c.finder.Datastore(ctx, datastore)
	if err != nil {
		return nil, fmt.Errorf("datastore %q: %w", datastore, err)
	}
	browser, err := ds.Browser(ctx)
	if err != nil {
		return nil, fmt.Errorf("browser: %w", err)
	}

	// Step 1: list direct children of cluster-installer/
	rootSpec := types.HostDatastoreBrowserSearchSpec{
		MatchPattern: []string{"*"},
		Details:      &types.FileQueryFlags{FileType: true, FileSize: true},
	}
	rootTask, err := browser.SearchDatastore(ctx, ds.Path("cluster-installer"), &rootSpec)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", "cluster-installer", err)
	}
	rootInfo, err := rootTask.WaitForResult(ctx, nil)
	if err != nil {
		// "FileNotFound" → directory absent → no leftovers.
		return nil, nil
	}
	root, ok := rootInfo.Result.(types.HostDatastoreBrowserSearchResults)
	if !ok {
		return nil, fmt.Errorf("unexpected root search result type")
	}

	out := make([]LeftoverEntry, 0)
	for _, f := range root.File {
		fi := f.GetFileInfo()
		if fi == nil || fi.Path == "" || fi.Path == "." || fi.Path == ".." {
			continue
		}
		// Only descend into directories; files at the cluster-installer/
		// root are unexpected but we skip them rather than recurse.
		if _, isFolder := f.(*types.FolderFileInfo); !isFolder {
			continue
		}
		entry := LeftoverEntry{Path: fi.Path, Owned: ourRunIDs[fi.Path]}

		// Step 2: list children of cluster-installer/<run-id>/
		childRel := "cluster-installer/" + fi.Path
		childSpec := types.HostDatastoreBrowserSearchSpec{
			MatchPattern: []string{"*"},
			Details:      &types.FileQueryFlags{FileType: true, FileSize: true},
		}
		childTask, err := browser.SearchDatastore(ctx, ds.Path(childRel), &childSpec)
		if err != nil {
			entry.Error = err.Error()
			out = append(out, entry)
			continue
		}
		childInfo, err := childTask.WaitForResult(ctx, nil)
		if err != nil {
			entry.Error = err.Error()
			out = append(out, entry)
			continue
		}
		childRes, ok := childInfo.Result.(types.HostDatastoreBrowserSearchResults)
		if !ok {
			out = append(out, entry)
			continue
		}
		for _, cf := range childRes.File {
			cfi := cf.GetFileInfo()
			if cfi == nil || cfi.Path == "" {
				continue
			}
			entry.Files = append(entry.Files, LeftoverFile{Name: cfi.Path, Size: cfi.FileSize})
		}
		out = append(out, entry)
	}
	return out, nil
}

// WipeOwnedRunDirs deletes ONLY the cluster-installer/<run-id>/
// directories whose run-id is in ourRunIDs. Unknown run-ids (not in
// the wizard's local runs/ tree) are left strictly alone — they may
// belong to a parallel installer instance, a different operator, or
// another tool that uses the same `cluster-installer/` prefix.
//
// Returns the number of directories actually wiped (Owned + present).
//
// Why surgical instead of "wipe everything under cluster-installer/":
// the previous bulk approach was unsafe — a `for d in
// /vmfs/volumes/*/cluster-installer/*; do rm -rf $d; done` would
// happily delete a parallel run's data. Operator was right to flag
// this. We now only act on ids the wizard knows are ours.
func (c *Client) WipeOwnedRunDirs(ctx context.Context, datastore string, ourRunIDs map[string]bool, emit func(string)) (int, error) {
	log := func(format string, a ...any) {
		if emit != nil {
			emit(fmt.Sprintf(format, a...))
		}
	}

	entries, err := c.ListClusterInstallerEntries(ctx, datastore, ourRunIDs)
	if err != nil {
		return 0, err
	}
	owned := 0
	unknown := 0
	for _, e := range entries {
		if e.Owned {
			owned++
		} else {
			unknown++
		}
	}
	log("wipe ISOs on [%s]: %d owned, %d unknown (left alone)", datastore, owned, unknown)
	if owned == 0 {
		return 0, nil
	}

	wiped := 0
	for _, e := range entries {
		if !e.Owned {
			continue
		}
		rel := "cluster-installer/" + e.Path
		res := c.DeleteDatastoreDir(ctx, datastore, rel, emit)
		if len(res.StillPresent) > 0 || len(res.Errors) > 0 {
			log("wipe ISOs: %s — %d lingering, errors=%v (continuing)", rel, len(res.StillPresent), res.Errors)
		} else {
			wiped++
		}
	}

	// Try to remove the cluster-installer/ parent if it's now empty.
	// vSphere's rmdir succeeds only on an empty folder, so this is
	// safe even when UNKNOWN entries remain — the rmdir will fail
	// and the parent stays. We log either outcome.
	if err := c.TryRemoveEmptyDir(ctx, datastore, "cluster-installer"); err != nil {
		log("wipe ISOs: cluster-installer/ parent kept (not empty or rmdir err: %v)", err)
	} else {
		log("wipe ISOs: cluster-installer/ parent also removed (was empty)")
	}

	return wiped, nil
}

// powerOffForReconfigure brings the VM down so a hot-restricted device
// reconfigure (here: CD-ROM remove) can proceed. Tries graceful
// shutdown via VMware tools first — open-vm-tools is in our autoinstall
// package list and the verify stage already confirmed the system is
// fully up. Falls back to a hard power-off if tools don't ack within
// 60 s.
func powerOffForReconfigure(ctx context.Context, vm *object.VirtualMachine, log func(string, ...any)) error {
	log("eject CD: graceful shutdown via VMware tools")
	if err := vm.ShutdownGuest(ctx); err == nil {
		// Poll powerState every 1 s, up to 60 s.
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			var p mo.VirtualMachine
			if err := vm.Properties(ctx, vm.Reference(), []string{"runtime.powerState"}, &p); err == nil {
				if p.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOff {
					log("eject CD: VM gracefully powered off")
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
		log("eject CD: graceful shutdown didn't complete in 60s — using hard power off")
	} else {
		log("eject CD: graceful shutdown unavailable (%v) — using hard power off", err)
	}
	task, err := vm.PowerOff(ctx)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// findVMByName lists every VM under the bound datacenter and returns
// the one whose ObjectName matches exactly. Avoids govmomi finder's
// path syntax (where "@" splits VM-name from datacenter-name).
// Returns (nil, nil) when no VM matches — the caller treats that as
// benign skip.
func (c *Client) findVMByName(ctx context.Context, name string) (*object.VirtualMachine, error) {
	vms, err := c.finder.VirtualMachineList(ctx, "*")
	if err != nil {
		// "no virtual machines found" is a govmomi err; treat as miss.
		if _, ok := err.(*find.NotFoundError); ok {
			return nil, nil
		}
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	for _, vm := range vms {
		n, err := vm.ObjectName(ctx)
		if err != nil {
			continue
		}
		if n == name {
			return vm, nil
		}
	}
	return nil, nil
}

// WipeResult is a structured report from DeleteDatastoreDir. Caller
// (postInstallESXiCleanup, WipeAllClusterInstallerEntries) writes it
// to app.log so silent failures are no longer invisible.
type WipeResult struct {
	Datastore     string
	Path          string
	DsPath        string   // exact "[datastore] path" string we passed to govmomi
	SearchOK      bool     // did the initial SearchDatastore succeed?
	SearchError   string   // first error encountered during search (if any)
	DirExisted    bool     // search returned a populated result
	FilesFound    []string
	FilesDeleted  []string
	FilesFailed   []string // failed file paths
	DirRemoved    bool
	StillPresent  []string // files still on disk after our delete loop (vSphere silent-fail diagnostic)
	Errors        []string
}

// DeleteDatastoreDir wipes a directory on a datastore — every file
// inside, then the (now-empty) directory itself. Returns a structured
// WipeResult so the caller can log/persist exactly what happened.
//
// Why list+per-file instead of "delete the dir":
// vSphere's DeleteDatastoreFile_Task documents: "If a directory is to
// be deleted, the directory must be empty." A single rmdir on a
// populated tree silently no-ops on some vCenters. Fix: browser-list,
// delete each file individually, then remove the empty parent.
//
// Why the post-delete verify:
// In the field we observed DeleteDatastoreFile_Task returning Success
// while the file remained on the VMFS volume — likely a stale
// vSphere file lock that didn't release fast enough. After deleting
// every file, we re-search the directory; anything still present
// surfaces in WipeResult.StillPresent so the operator (and the SSH
// fallback path) can act on it.
func (c *Client) DeleteDatastoreDir(ctx context.Context, datastore, dsRel string, emit func(string)) WipeResult {
	res := WipeResult{Datastore: datastore, Path: dsRel}
	log := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		if emit != nil {
			emit(line)
		}
	}
	addErr := func(format string, a ...any) {
		res.Errors = append(res.Errors, fmt.Sprintf(format, a...))
	}

	ds, err := c.finder.Datastore(ctx, datastore)
	if err != nil {
		log("cleanup ISOs: datastore %q not found (%v) — skipping", datastore, err)
		addErr("datastore lookup: %v", err)
		res.SearchError = err.Error()
		return res
	}
	cleanRel := path.Clean(dsRel)
	dsPath := ds.Path(cleanRel)
	res.DsPath = dsPath
	fm := object.NewFileManager(c.gc.Client)

	// 1. List directory contents.
	browser, err := ds.Browser(ctx)
	if err != nil {
		log("cleanup ISOs: browser unavailable (%v)", err)
		addErr("browser: %v", err)
		res.SearchError = err.Error()
		return res
	}
	listFiles := func() ([]string, bool, error) {
		spec := types.HostDatastoreBrowserSearchSpec{
			MatchPattern: []string{"*"},
			Details:      &types.FileQueryFlags{FileType: true, FileSize: false},
		}
		t, err := browser.SearchDatastore(ctx, dsPath, &spec)
		if err != nil {
			return nil, false, fmt.Errorf("search submit: %w", err)
		}
		info, err := t.WaitForResult(ctx, nil)
		if err != nil {
			return nil, false, fmt.Errorf("search wait: %w", err)
		}
		r, ok := info.Result.(types.HostDatastoreBrowserSearchResults)
		if !ok {
			return nil, true, fmt.Errorf("unexpected search result type %T", info.Result)
		}
		var names []string
		for _, f := range r.File {
			fi := f.GetFileInfo()
			if fi == nil || fi.Path == "" || fi.Path == "." || fi.Path == ".." {
				continue
			}
			names = append(names, fi.Path)
		}
		return names, true, nil
	}

	files, _, err := listFiles()
	if err != nil {
		// Don't pre-judge "FileNotFound" — log the actual error text so
		// we can distinguish a genuine absent directory from a govmomi
		// quirk (path-format mismatch, datacenter routing issue, soap
		// fault, …). The caller's SSH fallback decides what to do.
		res.SearchError = err.Error()
		log("cleanup ISOs: search %s failed: %v", dsPath, err)
		addErr("search %s: %v", dsPath, err)
		return res
	}
	res.SearchOK = true
	res.DirExisted = true
	res.FilesFound = files
	log("cleanup ISOs: %s contains %d file(s)", dsPath, len(files))

	// 2. Delete each file. After each delete we trust vSphere's
	//    Task.Wait error reporting — but we'll re-list at the end to
	//    catch the silent-success case where the file lingers.
	for _, fileName := range files {
		fullRel := cleanRel + "/" + fileName
		fullPath := ds.Path(fullRel)
		delTask, err := fm.DeleteDatastoreFile(ctx, fullPath, c.dc)
		if err != nil {
			log("cleanup ISOs: delete %s — submit failed: %v", fullRel, err)
			addErr("delete %s submit: %v", fileName, err)
			res.FilesFailed = append(res.FilesFailed, fileName)
			continue
		}
		if err := delTask.Wait(ctx); err != nil {
			log("cleanup ISOs: delete %s — wait failed: %v", fullRel, err)
			addErr("delete %s wait: %v", fileName, err)
			res.FilesFailed = append(res.FilesFailed, fileName)
			continue
		}
		log("cleanup ISOs: removed %s/%s", cleanRel, fileName)
		res.FilesDeleted = append(res.FilesDeleted, fileName)
	}

	// 3. Verify everything is actually gone. vSphere has been observed
	//    returning Success on DeleteDatastoreFile_Task while the file
	//    remained on the VMFS volume — the silent-failure case the
	//    operator was hitting.
	if len(files) > 0 {
		// Brief settle delay before re-listing.
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			addErr("post-delete verify: ctx cancelled")
			return res
		}
		stillThere, _, _ := listFiles()
		if len(stillThere) > 0 {
			res.StillPresent = stillThere
			log("cleanup ISOs: %d file(s) still present after delete: %v", len(stillThere), stillThere)
			addErr("post-delete verify: %d file(s) lingering: %v", len(stillThere), stillThere)
		}
	}

	// 4. Remove the now-empty directory (only if we actually emptied it).
	if len(res.StillPresent) == 0 {
		rmTask, err := fm.DeleteDatastoreFile(ctx, dsPath, c.dc)
		if err != nil {
			log("cleanup ISOs: rmdir %s — submit failed: %v", cleanRel, err)
			addErr("rmdir submit: %v", err)
			return res
		}
		if err := rmTask.Wait(ctx); err != nil {
			log("cleanup ISOs: rmdir %s — wait failed: %v", cleanRel, err)
			addErr("rmdir wait: %v", err)
			return res
		}
		log("cleanup ISOs: removed directory %s", cleanRel)
		res.DirRemoved = true
	} else {
		log("cleanup ISOs: leaving directory %s (lingering files)", cleanRel)
	}
	return res
}
