// Package runner wraps the embedded terraform.exe and ansible-playbook.exe
// with structured progress reporting so the Wails frontend can stream events.
package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cmars-devops/cluster-installer/internal/runtime"
)

// TFRun is a single terraform invocation.
type TFRun struct {
	StackDir   string            // directory containing main.tf
	VarFile    string            // path to tfvars.json
	Env        map[string]string // additional env vars (TF_PLUGIN_CACHE_DIR, etc.)
	OnLine     func(string)
}

func (r *TFRun) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, runtime.TerraformBinPath(), args...)
	c.Dir = r.StackDir
	env := os.Environ()
	env = append(env, "TF_PLUGIN_CACHE_DIR="+runtime.ProviderCache())
	env = append(env, "TF_IN_AUTOMATION=1")
	env = append(env, "CHECKPOINT_DISABLE=1")
	for k, v := range r.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	c.Env = env
	return c
}

func (r *TFRun) Init(ctx context.Context) error {
	return r.stream(r.cmd(ctx, "init", "-input=false", "-no-color"))
}

func (r *TFRun) Plan(ctx context.Context, planOut string) error {
	args := []string{"plan", "-input=false", "-no-color", "-out=" + planOut}
	if r.VarFile != "" {
		args = append(args, "-var-file="+r.VarFile)
	}
	return r.stream(r.cmd(ctx, args...))
}

func (r *TFRun) Apply(ctx context.Context, planFile string) error {
	args := []string{"apply", "-input=false", "-no-color", "-auto-approve"}
	if planFile != "" {
		args = append(args, planFile)
	} else if r.VarFile != "" {
		args = append(args, "-var-file="+r.VarFile)
	}
	return r.stream(r.cmd(ctx, args...))
}

func (r *TFRun) stream(cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	go pipeLines(stdout, r.OnLine)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("terraform %s: %w", filepath.Base(cmd.Path), err)
	}
	return nil
}

func pipeLines(rd io.Reader, sink func(string)) {
	if sink == nil {
		_, _ = io.Copy(io.Discard, rd)
		return
	}
	br := bufio.NewScanner(rd)
	br.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for br.Scan() {
		sink(br.Text())
	}
}
