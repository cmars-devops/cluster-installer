// Package runtime owns the per-user "%LOCALAPPDATA%\cluster-installer" tree.
// Nothing in this package writes to system locations; the installer is fully
// portable and runs without admin rights.
package runtime

import (
	"os"
	"path/filepath"
)

// AppDataDir is the root of the per-user installer state.
func AppDataDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "cluster-installer")
}

func RuntimeDir() string  { return filepath.Join(AppDataDir(), "runtime") }
func ContentDir() string  { return filepath.Join(AppDataDir(), "content") }
func RunsDir() string     { return filepath.Join(AppDataDir(), "runs") }
func ProviderCache() string { return filepath.Join(AppDataDir(), "cache", "providers") }
func BinDir() string      { return filepath.Join(AppDataDir(), "bin") }

// EnsureDirs creates the entire tree on first run.
func EnsureDirs() error {
	for _, d := range []string{
		AppDataDir(), RuntimeDir(), ContentDir(), RunsDir(),
		ProviderCache(), BinDir(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
