package imagecache

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	apruntime "github.com/cmars-devops/cluster-installer/internal/runtime"
)

// RemasterUbuntuISO produces a per-node Ubuntu Server live-server ISO at
// dstPath whose GRUB menu entries carry the Subiquity autoinstall cmdline
// pointing at the wizard's HTTP-served NoCloud datasource:
//
//	autoinstall ds=nocloud-net;s=<dataSourceURL>
//
// The dataSourceURL must end with a trailing slash — cloud-init treats it
// as a directory containing user-data + meta-data.
//
// Implementation: shells out to a uv-managed Python script using pycdlib.
// We tried pure-Go go-diskfs first; it silently dropped file content when
// rebuilding 3 GB hybrid ISOs (every casper/EFI file was lost), so the
// resulting ISO had nothing to boot. pycdlib is the de-facto library for
// this job (Anaconda + most distro tooling use it) and reliably preserves
// El Torito BIOS + UEFI boot records on rebuild.
//
// Cost: first invocation downloads pycdlib via uv (~5 s, cached after);
// subsequent invocations are pure I/O (~30-60 s for a 3 GB ISO).
func RemasterUbuntuISO(ctx context.Context, srcPath, dstPath, dataSourceURL string, progress Progress) error {
	emit(progress, "%s  ubuntu remastering → %s", path.Base(srcPath), path.Base(dstPath))

	// Idempotent: clear leftover from a previously failed remaster.
	_ = os.Remove(dstPath)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dstPath), err)
	}

	uvPath := filepath.Join(apruntime.BinDir(), "uv.exe")
	if _, err := os.Stat(uvPath); err != nil {
		return fmt.Errorf("uv not extracted: %w", err)
	}
	scriptPath, err := remasterScriptPath()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, uvPath,
		"run", "--quiet", "--with", "pycdlib",
		"python", scriptPath,
		"--src", srcPath,
		"--dst", dstPath,
		"--url", dataSourceURL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pycdlib remaster: %w\noutput:\n%s", err, string(out))
	}
	for _, line := range splitLines(out) {
		emit(progress, "  %s", line)
	}
	emit(progress, "%s  ubuntu remaster done", path.Base(dstPath))
	return nil
}

// remasterScriptPath returns the path to remaster.py inside the active
// content tree (downloaded by Step 1 to %LOCALAPPDATA%\cluster-installer\
// content\<ref>\seeds\autoinstall\remaster.py). The orchestrator passes
// ContentDir at construction time, but this package doesn't take it as
// a parameter — instead we look up the most recent content extract.
func remasterScriptPath() (string, error) {
	contentRoot := apruntime.ContentDir()
	entries, err := os.ReadDir(contentRoot)
	if err != nil {
		return "", fmt.Errorf("read content root: %w", err)
	}
	// Prefer the most-recently-modified ref directory (last fetched).
	var newest os.DirEntry
	var newestMod int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Unix() > newestMod {
			newest = e
			newestMod = info.ModTime().Unix()
		}
	}
	if newest == nil {
		return "", fmt.Errorf("no content ref found under %s", contentRoot)
	}
	candidate := filepath.Join(contentRoot, newest.Name(), "seeds", "autoinstall", "remaster.py")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("remaster script missing: %s", candidate)
	}
	return candidate, nil
}

func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			s := string(b[start:i])
			if len(s) > 0 && s[len(s)-1] == '\r' {
				s = s[:len(s)-1]
			}
			if s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if start < len(b) {
		s := string(b[start:])
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
