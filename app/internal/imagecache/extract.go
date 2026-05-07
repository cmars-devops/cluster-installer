package imagecache

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"
)

// AgamaBoot is the trio of files Agama needs for direct kernel boot. The
// orchestrator's HTTP server (rooted at runs/<id>/staging/) serves them at
// the URLs the libvirt domain's <kernel>/<initrd>/<cmdline> elements
// reference.
type AgamaBoot struct {
	Vmlinuz  string // <repoDir>/vmlinuz
	Initrd   string // <repoDir>/initrd
	Squashfs string // <repoDir>/LiveOS/squashfs.img
}

// extractTargets maps logical artefact name → list of in-ISO paths to try
// in order. openSUSE NET ISOs use Rock Ridge so the lowercase paths land
// first; the uppercase variants are AutoYaST-era fallbacks for older or
// non-RR images that fall back to the ISO9660 8.3 directory.
var extractTargets = map[string][]string{
	"vmlinuz":  {"/boot/x86_64/loader/linux", "/BOOT/X86_64/LOADER/LINUX"},
	"initrd":   {"/boot/x86_64/loader/initrd", "/BOOT/X86_64/LOADER/INITRD"},
	"squashfs": {"/LiveOS/squashfs.img", "/LIVEOS/SQUASHFS.IMG"},
}

// ExtractAgamaBoot reads vmlinuz + initrd + squashfs out of the cached
// ISO and writes them under repoDir in the layout the seed templates
// reference (repoDir/vmlinuz, repoDir/initrd, repoDir/LiveOS/squashfs.img).
//
// Idempotent: existing extracted files newer than the source ISO are
// reused — re-runs of the same content tag pay the extraction cost
// only once.
func ExtractAgamaBoot(ctx context.Context, isoPath, repoDir string, progress Progress) (AgamaBoot, error) {
	out := AgamaBoot{
		Vmlinuz:  filepath.Join(repoDir, "vmlinuz"),
		Initrd:   filepath.Join(repoDir, "initrd"),
		Squashfs: filepath.Join(repoDir, "LiveOS", "squashfs.img"),
	}

	if alreadyExtracted(isoPath, out) {
		emit(progress, "%s  extract cache hit", filepath.Base(isoPath))
		return out, nil
	}

	if err := os.MkdirAll(filepath.Join(repoDir, "LiveOS"), 0o755); err != nil {
		return AgamaBoot{}, err
	}

	disk, err := diskfs.Open(isoPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return AgamaBoot{}, fmt.Errorf("open iso %s: %w", isoPath, err)
	}
	defer disk.File.Close()

	fs, err := disk.GetFilesystem(0)
	if err != nil {
		return AgamaBoot{}, fmt.Errorf("get fs: %w", err)
	}

	dstPaths := map[string]string{
		"vmlinuz":  out.Vmlinuz,
		"initrd":   out.Initrd,
		"squashfs": out.Squashfs,
	}
	for _, name := range []string{"vmlinuz", "initrd", "squashfs"} {
		if err := ctx.Err(); err != nil {
			return AgamaBoot{}, err
		}
		emit(progress, "%s  extracting %s", filepath.Base(isoPath), name)
		if err := extractOne(fs, extractTargets[name], dstPaths[name]); err != nil {
			return AgamaBoot{}, fmt.Errorf("extract %s: %w", name, err)
		}
	}
	emit(progress, "%s  extract complete", filepath.Base(isoPath))
	return out, nil
}

// extractOne tries each candidate in turn, copying the first one that
// opens to dst. Returns the last error if none worked.
func extractOne(fs filesystem.FileSystem, candidates []string, dst string) error {
	var lastErr error
	for _, src := range candidates {
		f, err := fs.OpenFile(src, os.O_RDONLY)
		if err != nil {
			lastErr = err
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := io.Copy(out, f); err != nil {
			_ = f.Close()
			_ = out.Close()
			_ = os.Remove(dst)
			return err
		}
		_ = f.Close()
		if err := out.Close(); err != nil {
			_ = os.Remove(dst)
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("none of %v exist in iso", candidates)
	}
	return lastErr
}

// alreadyExtracted returns true if all three target files exist and are
// each at least as new as the source ISO. mtime-only check is sufficient
// because the source ISO is itself sha256-verified before we get here,
// so its mtime is a reliable cache key.
func alreadyExtracted(isoPath string, b AgamaBoot) bool {
	src, err := os.Stat(isoPath)
	if err != nil {
		return false
	}
	srcMod := src.ModTime()
	for _, p := range []string{b.Vmlinuz, b.Initrd, b.Squashfs} {
		fi, err := os.Stat(p)
		if err != nil || fi.ModTime().Before(srcMod) {
			return false
		}
	}
	return true
}
