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
	"github.com/triangles-co-kr/cluster-installer/internal/logging"
	"github.com/triangles-co-kr/cluster-installer/internal/runtime"
)

// Fetch returns the absolute filesystem path of the content repo checked out
// at the given ref (tag, branch, or commit). The result is cached per-ref under
// %LOCALAPPDATA%\cluster-installer\content\<ref>\.
func Fetch(ctx context.Context, repoURL, ref string, log *logging.Logger) (string, error) {
	if repoURL == "" {
		repoURL = "https://github.com/triangles-co-kr/cluster-installer-content.git"
	}
	if ref == "" {
		return "", fmt.Errorf("content ref is required")
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
