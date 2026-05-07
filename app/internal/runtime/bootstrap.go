package runtime

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/triangles-co-kr/cluster-installer/internal/logging"
)

// Status reports the readiness of the per-user runtime.
type Status struct {
	UvInstalled         bool   `json:"uv_installed"`
	UvVersion           string `json:"uv_version"`
	AnsibleCoreInstalled bool  `json:"ansible_core_installed"`
	AnsibleCoreVersion  string `json:"ansible_core_version"`
	TerraformInstalled  bool   `json:"terraform_installed"`
	TerraformVersion    string `json:"terraform_version"`
	BootstrapMessage    string `json:"bootstrap_message"`
}

// ExtractEmbeddedBinaries copies the bundled terraform.exe and uv.exe out of
// the Go embed.FS into %LOCALAPPDATA%\cluster-installer\bin\ on first run.
// Subsequent launches no-op when the target file exists with the same size.
func ExtractEmbeddedBinaries(efs embed.FS) error {
	if err := EnsureDirs(); err != nil {
		return err
	}
	for _, name := range []string{"terraform.exe", "uv.exe"} {
		src, err := efs.Open(filepath.ToSlash(filepath.Join("embedded", "bin", name)))
		if err != nil {
			// Tolerate missing-during-development; CI build must populate this.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("open embedded %s: %w", name, err)
		}
		dstPath := filepath.Join(BinDir(), name)
		if fi, err := os.Stat(dstPath); err == nil && fi.Size() > 0 {
			_ = src.Close()
			continue
		}
		dst, err := os.Create(dstPath)
		if err != nil {
			_ = src.Close()
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			_ = src.Close()
			_ = dst.Close()
			return err
		}
		_ = src.Close()
		_ = dst.Close()
	}
	return nil
}

// EnsureReady probes uv + ansible-core, installing them on demand. Idempotent.
func EnsureReady(ctx context.Context, log *logging.Logger) (Status, error) {
	s := Status{}
	if v, err := runOut(ctx, filepath.Join(BinDir(), "uv.exe"), "--version"); err == nil {
		s.UvInstalled, s.UvVersion = true, v
	} else {
		s.BootstrapMessage = "uv not found — bundle missing or extraction failed"
		return s, err
	}

	// uv installs ansible-core into a managed venv, isolated from system Python.
	venv := filepath.Join(RuntimeDir(), "ansible-venv")
	if _, err := os.Stat(filepath.Join(venv, "Scripts", "ansible.exe")); err != nil {
		log.Info("bootstrap", "msg", "installing ansible-core via uv", "venv", venv)
		cmd := exec.CommandContext(ctx,
			filepath.Join(BinDir(), "uv.exe"),
			"venv", "--python", "3.12", venv,
		)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return s, fmt.Errorf("uv venv: %w", err)
		}
		cmd = exec.CommandContext(ctx,
			filepath.Join(BinDir(), "uv.exe"),
			"pip", "install",
			"--python", filepath.Join(venv, "Scripts", "python.exe"),
			"ansible-core==2.17.*",
		)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return s, fmt.Errorf("uv pip install ansible-core: %w", err)
		}
	}

	if v, err := runOut(ctx, filepath.Join(venv, "Scripts", "ansible.exe"), "--version"); err == nil {
		s.AnsibleCoreInstalled, s.AnsibleCoreVersion = true, v
	}

	if v, err := runOut(ctx, filepath.Join(BinDir(), "terraform.exe"), "version"); err == nil {
		s.TerraformInstalled, s.TerraformVersion = true, v
	}

	return s, nil
}

// AnsibleBinPath returns the absolute path to ansible-playbook.exe in the
// managed venv. Used by the runner package.
func AnsibleBinPath() string {
	return filepath.Join(RuntimeDir(), "ansible-venv", "Scripts", "ansible-playbook.exe")
}

// TerraformBinPath returns the absolute path to terraform.exe.
func TerraformBinPath() string {
	return filepath.Join(BinDir(), "terraform.exe")
}

func runOut(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
