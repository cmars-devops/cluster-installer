// Package content fetches the IaC content repo at runtime via go-git so the
// installer exe stays thin and can roll forward without rebuilding.
package content

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/cmars-devops/cluster-installer/internal/logging"
	"github.com/cmars-devops/cluster-installer/internal/runtime"
)

// Fetch returns the absolute filesystem path of the content repo checked out
// at the given ref (tag, branch, or commit). The result is cached per-ref under
// %LOCALAPPDATA%\cluster-installer\content\<ref>\.
//
// Dev override: when the exe sits next to a content/ directory that already
// looks like a real checkout (images.yaml + terraform/ tree present), that
// path wins regardless of `ref`. This lets local development iterate on the
// content submodule WITHOUT push → tag → fetch round-trips — the operator
// just rebuilds the exe and stack changes take effect on the next Apply.
// End users who copy cluster-installer.exe to a clean folder don't have a
// content/ sibling, so they fall through to the clone path unchanged.
func Fetch(ctx context.Context, repoURL, ref string, log *logging.Logger) (string, error) {
	if repoURL == "" {
		repoURL = "https://github.com/cmars-devops/cluster-installer-content.git"
	}
	if ref == "" {
		return "", fmt.Errorf("content ref is required")
	}
	if local, ok := localCheckout(); ok {
		log.Info("content.fetch", "msg", "using local checkout (dev mode)", "ref", ref, "dst", local)
		return local, nil
	}
	dst := filepath.Join(runtime.ContentDir(), ref)
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		return dst, nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}

	log.Info("content.fetch", "repo", repoURL, "ref", ref, "dst", dst)
	_, err := git.PlainCloneContext(ctx, dst, false, &git.CloneOptions{
		URL:           repoURL,
		ReferenceName: plumbing.NewTagReferenceName(ref),
		Depth:         1,
		SingleBranch:  true,
		Tags:          git.NoTags,
	})
	if err == nil {
		return dst, nil
	}
	// Fall back to branch ref if tag clone failed.
	log.Info("content.fetch", "msg", "tag clone failed, trying branch", "err", err)
	_, err = git.PlainCloneContext(ctx, dst, false, &git.CloneOptions{
		URL:           repoURL,
		ReferenceName: plumbing.NewBranchReferenceName(ref),
		Depth:         1,
		SingleBranch:  true,
	})
	if err != nil {
		return "", fmt.Errorf("clone %s @ %s: %w", repoURL, ref, err)
	}
	return dst, nil
}

// localCheckout looks for a content/ directory next to (or one level
// above) the running executable that smells like a real checkout. When
// found we use it verbatim — no clone, no caching — so changes the
// developer makes in the working tree take effect on the next Apply.
//
// Detection markers: images.yaml (every content release has one) and
// terraform/stacks/esxi/main.tf (the stack the orchestrator actually
// renders against). Both must be present to count; an empty dir or a
// half-deleted tree falls back to clone.
func localCheckout() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, "content"),                                 // exe sibling — `cluster-installer.exe` at repo root
		filepath.Join(exeDir, "..", "content"),                           // exe one level deep
		filepath.Join(exeDir, "..", "..", "..", "content"),               // app/build/bin/cluster-installer.exe → ../../../content
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if hasContentMarkers(abs) {
			return abs, true
		}
	}
	return "", false
}

// hasContentMarkers reports whether dir contains the two files the
// orchestrator and image cache actually consume. Anything missing →
// don't claim this is a usable checkout.
func hasContentMarkers(dir string) bool {
	for _, marker := range []string{
		"images.yaml",
		filepath.Join("terraform", "stacks", "esxi", "main.tf"),
	} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return false
		}
	}
	return true
}
