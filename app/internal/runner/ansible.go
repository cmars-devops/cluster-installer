package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/triangles-co-kr/cluster-installer/internal/runtime"
)

// AnsibleRun executes one playbook from the content tree.
type AnsibleRun struct {
	ContentDir   string            // resolved content path (with /ansible/ playbooks)
	Playbook     string            // e.g. "playbooks/00-preflight.yml"
	InventoryYAML string           // absolute path of rendered hosts.yml
	ExtraVars    map[string]string // -e key=value
	SSHKeyPath   string
	OnLine       func(string)
}

func (r *AnsibleRun) Run(ctx context.Context) error {
	args := []string{
		"-i", r.InventoryYAML,
		"--ssh-extra-args", "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
		r.Playbook,
	}
	for k, v := range r.ExtraVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.CommandContext(ctx, runtime.AnsibleBinPath(), args...)
	cmd.Dir = r.ContentDir + string(os.PathSeparator) + "ansible"
	env := os.Environ()
	env = append(env, "ANSIBLE_FORCE_COLOR=0")
	env = append(env, "ANSIBLE_NOCOWS=1")
	env = append(env, "ANSIBLE_SSH_KEY_PATH="+r.SSHKeyPath)
	env = append(env, "ANSIBLE_STDOUT_CALLBACK=yaml")
	cmd.Env = env

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
		return fmt.Errorf("ansible-playbook %s: %w", r.Playbook, err)
	}
	return nil
}
