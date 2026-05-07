// Package run owns the "Apply" lifecycle of a single wizard run.
// It binds the embedded HTTP server to a host IP, renders per-node Agama
// profiles + Combustion ISOs into a staging directory the server serves
// from, drives terraform/ansible, and tears the server down at the end.
//
// Stage order (each stage emits a "run:stage" event):
//
//   1. start_http   bind 0.0.0.0:<ephemeral>, advertise <hostIP>:<port>
//   2. render_seeds Agama JSON → staging/profiles/, Combustion ISO → staging/seeds/
//   3. terraform_init/plan/apply
//   4. wait_ssh     poll port 22 on every node IP
//   5. ansible      00-preflight → 10-ceph → 20-(rke2|k3s) → 30-csi → 40-addons
//   6. finalize     stop HTTP server, fetch kubeconfig + dashboard creds
//
// On any failure the server is stopped and the run is marked Failed in
// state.Run; the user can resume by re-entering the wizard.
package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cmars-devops/cluster-installer/internal/httpserve"
	"github.com/cmars-devops/cluster-installer/internal/inventory"
	"github.com/cmars-devops/cluster-installer/internal/logging"
	"github.com/cmars-devops/cluster-installer/internal/netutil"
	"github.com/cmars-devops/cluster-installer/internal/runner"
	apruntime "github.com/cmars-devops/cluster-installer/internal/runtime"
	"github.com/cmars-devops/cluster-installer/internal/seed"
	"github.com/cmars-devops/cluster-installer/internal/state"
)

// Emitter is the surface the orchestrator uses to push progress to the UI.
// In production this is wired to wailsruntime.EventsEmit; in tests it's a
// channel-based stub.
type Emitter interface {
	Emit(name string, data ...interface{})
}

// Orchestrator owns one run's execution.
type Orchestrator struct {
	Run        *state.Run
	ContentDir string         // resolved content tag dir
	Inventory  inventory.Inventory
	Store      *state.Store
	Log        *logging.Logger
	Events     Emitter

	// populated during execution
	httpSrv     *httpserve.Server
	stagingDir  string
	hostIP      string
	baseURL     string
}

// Apply runs the whole pipeline. Cancelling ctx tears down the HTTP server
// and aborts at the next stage boundary. The exact stage list depends on
// cluster.topology — see pipelineStages.
func (o *Orchestrator) Apply(ctx context.Context) error {
	defer func() {
		if o.httpSrv != nil {
			_ = o.httpSrv.Stop()
			o.emit("run:stage", string(state.StageCompleted), "http server stopped")
		}
	}()

	// Pre-allocate MACs so seeds and tfvars see the same value. Idempotent:
	// nodes that already carry a MAC (e.g. from a resumed run) are left
	// untouched. Persist any change to run.json before stages start.
	if changed := ensureNodeMACs(&o.Inventory); changed {
		if err := o.Store.Update(o.Run.ID, func(r *state.Run) {
			r.Inventory = o.Inventory
		}); err != nil {
			return fmt.Errorf("persist mac allocation: %w", err)
		}
		o.emit("run:line", "→ pre-allocated MACs for seed/TF binding")
	}

	for _, st := range o.pipelineStages() {
		if err := ctx.Err(); err != nil {
			return o.fail(st.name, err)
		}
		o.markStage(st.name)
		if err := st.fn(ctx); err != nil {
			return o.fail(st.name, err)
		}
	}

	o.markStage(state.StageCompleted)
	return nil
}

// stage couples a state.Stage label with its execution function.
type stage struct {
	name state.Stage
	fn   func(context.Context) error
}

// pipelineStages tailors the stage list to cluster.topology:
//
//	combined  (default): all stages — seeds → TF → SSH → preflight → ceph → k8s → csi → addons
//	ceph-only:           seeds → TF → SSH → preflight → ceph                 (no k8s, no csi, no addons)
//	k8s-only:            seeds → TF → SSH → preflight        → k8s → [csi]* → addons
//	                     *csi runs only when cluster.external_ceph is configured;
//	                     otherwise the cluster has no Ceph and CSI would have no
//	                     backend to bind.
//
// Skipped stages emit "run:stage-skipped" so the UI can render a strikethrough
// instead of leaving the user wondering whether the stage failed silently.
func (o *Orchestrator) pipelineStages() []stage {
	topo := o.Inventory.Cluster.Topology
	if topo == "" {
		topo = "combined" // back-compat: pre-topology inventories run the full pipeline
	}

	common := []stage{
		{state.StageSeedISO, o.startHTTPAndRenderSeeds},
		{state.StageTFInit, o.terraformInit},
		{state.StageTFPlan, o.terraformPlan},
		{state.StageTFApply, o.terraformApply},
		{state.StageWaitSSH, o.waitSSH},
		{state.StagePreflight, func(c context.Context) error { return o.runPlaybook(c, "playbooks/00-preflight.yml") }},
	}

	cephStage := stage{state.StageCeph, func(c context.Context) error { return o.runPlaybook(c, "playbooks/10-ceph-cephadm.yml") }}
	k8sStage := stage{state.StageK8s, o.runK8sPlaybook}
	csiStage := stage{state.StageCSI, func(c context.Context) error { return o.runPlaybook(c, "playbooks/30-csi-ceph.yml") }}
	addonsStage := stage{state.StageAddons, func(c context.Context) error { return o.runPlaybook(c, "playbooks/40-addons.yml") }}

	switch topo {
	case "ceph-only":
		o.skip(state.StageK8s, "topology=ceph-only")
		o.skip(state.StageCSI, "topology=ceph-only")
		o.skip(state.StageAddons, "topology=ceph-only")
		return append(common, cephStage)

	case "k8s-only":
		o.skip(state.StageCeph, "topology=k8s-only")
		out := append(common, k8sStage)
		if o.Inventory.Cluster.ExternalCeph != nil {
			out = append(out, csiStage)
		} else {
			o.skip(state.StageCSI, "topology=k8s-only without external_ceph")
		}
		return append(out, addonsStage)

	default: // "combined"
		return append(common, cephStage, k8sStage, csiStage, addonsStage)
	}
}

// skip records that a stage is intentionally not part of this pipeline. The
// UI can use these events to dim/strike skipped stages on the run-progress
// view, so users don't have to guess why "ceph" never appeared in the log.
func (o *Orchestrator) skip(s state.Stage, reason string) {
	o.emit("run:stage-skipped", string(s), reason)
	o.Log.Info("orchestrator.skip", "stage", string(s), "reason", reason)
}

// startHTTPAndRenderSeeds picks the host IP, starts the HTTP server, then
// renders Agama profiles and Combustion ISOs into the staging dir. The HTTP
// server stays up for the rest of the run because nodes may re-fetch
// profiles on installer retries.
func (o *Orchestrator) startHTTPAndRenderSeeds(ctx context.Context) error {
	// 1. Pick advertise IP (the Windows NIC that routes to the target).
	hostIP, err := netutil.PickAdvertiseIP(o.Inventory.Target.Endpoint)
	if err != nil {
		return fmt.Errorf("pick advertise IP: %w", err)
	}
	o.hostIP = hostIP

	// 2. Per-run staging directory served by the HTTP server.
	o.stagingDir = filepath.Join(apruntime.RunsDir(), o.Run.ID, "staging")
	for _, sub := range []string{"profiles", "seeds", "repo"} {
		if err := os.MkdirAll(filepath.Join(o.stagingDir, sub), 0o755); err != nil {
			return fmt.Errorf("mkdir staging/%s: %w", sub, err)
		}
	}

	// 3. Start HTTP server (ephemeral port).
	o.httpSrv = &httpserve.Server{Root: o.stagingDir, Bind: "0.0.0.0:0"}
	go func() {
		// Serve blocks until ctx cancels. Errors are logged; the run will
		// fail naturally if VMs can't fetch profiles.
		if err := o.httpSrv.Start(ctx); err != nil {
			o.Log.Error("httpserve", "err", err)
		}
	}()

	// Tiny delay so .URL() can read the bound addr.
	if err := o.httpSrv.WaitReady(ctx); err != nil {
		return err
	}
	o.baseURL = o.httpSrv.URL(o.hostIP)
	o.emit("run:server-listening", map[string]string{"url": o.baseURL})
	o.Log.Info("orchestrator", "msg", "http server listening", "url", o.baseURL)

	// 4. Windows Firewall hint — first inbound connection triggers a one-time
	// dialog; document this in the UI rather than try to add a rule
	// (rule-add requires admin and breaks the no-admin promise).
	o.emit("run:firewall-hint", map[string]string{
		"url":  o.baseURL,
		"note": "Windows 방화벽이 첫 inbound 연결 시 허용 대화상자를 띄울 수 있습니다 — '액세스 허용'을 클릭하세요.",
	})

	// 5. Render seed payloads per node.
	hostsEntries := seed.HostsEntriesFromInventory(o.Inventory)
	for _, n := range o.Inventory.Nodes {
		ctx := seed.BuildContext(o.Inventory, n, o.Run.RootPasswordHash, hostsEntries)
		switch n.OS {
		case "leap":
			if err := o.renderAgama(ctx, n, "leap.auto.json.tmpl"); err != nil {
				return err
			}
		case "tumbleweed":
			if err := o.renderAgama(ctx, n, "tumbleweed.auto.json.tmpl"); err != nil {
				return err
			}
		case "microos":
			if err := o.renderCombustion(ctx, n); err != nil {
				return err
			}
		default:
			return fmt.Errorf("node %s: unsupported os %q", n.Hostname, n.OS)
		}
	}
	return nil
}

func (o *Orchestrator) renderAgama(ctx seed.Context, n inventory.NodeSpec, tmplName string) error {
	tmplPath := filepath.Join(o.ContentDir, "seeds", "agama", tmplName)
	out, err := seed.RenderFile(tmplPath, ctx)
	if err != nil {
		return fmt.Errorf("render agama %s: %w", n.Hostname, err)
	}
	dst := filepath.Join(o.stagingDir, "profiles", n.Hostname+".json")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		return err
	}
	o.Log.Info("seed.agama", "host", n.Hostname, "url", o.baseURL+"/profiles/"+n.Hostname+".json")
	return nil
}

func (o *Orchestrator) renderCombustion(ctx seed.Context, n inventory.NodeSpec) error {
	tmplPath := filepath.Join(o.ContentDir, "seeds", "ignition", "combustion-script.tmpl")
	script, err := seed.RenderFile(tmplPath, ctx)
	if err != nil {
		return fmt.Errorf("render combustion %s: %w", n.Hostname, err)
	}
	ignTmpl := filepath.Join(o.ContentDir, "seeds", "ignition", "microos-base.ign.tmpl")
	ign, err := seed.RenderFile(ignTmpl, ctx)
	if err != nil {
		return fmt.Errorf("render ignition %s: %w", n.Hostname, err)
	}

	isoPath := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
	files := []seed.File{
		{Path: "combustion/script", Contents: script},
		{Path: "ignition/config.ign", Contents: ign},
	}
	if err := seed.Build(isoPath, seed.SeedIgnition, files); err != nil {
		return fmt.Errorf("build seed iso %s: %w", n.Hostname, err)
	}
	o.Log.Info("seed.combustion", "host", n.Hostname, "iso", isoPath)
	return nil
}

// ---- stage implementations -------------------------------------------

func (o *Orchestrator) terraformInit(ctx context.Context) error {
	stack, err := o.copyStackToRun()
	if err != nil {
		return err
	}
	if _, err := o.renderTFVars(); err != nil {
		return fmt.Errorf("render tfvars: %w", err)
	}
	r := o.tfRun(stack, "")
	return r.Init(ctx)
}

func (o *Orchestrator) terraformPlan(ctx context.Context) error {
	stack := o.tfStackDir()
	tfvars := filepath.Join(o.runDir(), "terraform", "tfvars.json")
	plan := filepath.Join(o.runDir(), "terraform", "plan.tfplan")
	r := o.tfRun(stack, tfvars)
	return r.Plan(ctx, plan)
}

func (o *Orchestrator) terraformApply(ctx context.Context) error {
	stack := o.tfStackDir()
	plan := filepath.Join(o.runDir(), "terraform", "plan.tfplan")
	r := o.tfRun(stack, "")
	if err := r.Apply(ctx, plan); err != nil {
		return err
	}
	o.emit("run:line", "terraform apply complete — VMs are booting and fetching profiles from "+o.baseURL)
	return nil
}

func (o *Orchestrator) waitSSH(ctx context.Context) error {
	hosts := make([]string, 0, len(o.Inventory.Nodes))
	for _, n := range o.Inventory.Nodes {
		hosts = append(hosts, n.IP)
	}
	o.emit("run:line", fmt.Sprintf("waiting for SSH on %d nodes (timeout 30m each)", len(hosts)))
	return runner.WaitForSSH(ctx, hosts, "root", o.sshKeyPath(), 30*time.Minute)
}

func (o *Orchestrator) runK8sPlaybook(ctx context.Context) error {
	pb := "playbooks/20-rke2.yml"
	if o.Inventory.Cluster.Kubernetes.Distro == "k3s" {
		pb = "playbooks/20-k3s.yml"
	}
	return o.runPlaybook(ctx, pb)
}

func (o *Orchestrator) runPlaybook(ctx context.Context, rel string) error {
	hostsPath, err := o.renderHostsYAML()
	if err != nil {
		return fmt.Errorf("render hosts.yml: %w", err)
	}
	// Bootstrap ansible-core in the user's runtime venv on first run.
	// Idempotent — skips when ansible-playbook.exe already exists.
	if _, err := apruntime.EnsureReady(ctx, o.Log); err != nil {
		return fmt.Errorf("ansible runtime: %w", err)
	}
	r := &runner.AnsibleRun{
		ContentDir:    o.ContentDir,
		Playbook:      rel,
		InventoryYAML: hostsPath,
		SSHKeyPath:    o.sshKeyPath(),
		OnLine:        func(line string) { o.emit("run:line", line) },
	}
	o.emit("run:line", "→ ansible-playbook "+rel)
	return r.Run(ctx)
}

// ---- terraform helpers -----------------------------------------------

func (o *Orchestrator) tfRun(stackDir, varFile string) *runner.TFRun {
	return &runner.TFRun{
		StackDir: stackDir,
		VarFile:  varFile,
		OnLine:   func(line string) { o.emit("run:line", line) },
	}
}

func (o *Orchestrator) tfStackDir() string {
	return filepath.Join(o.runDir(), "terraform", o.Inventory.Target.Type)
}

// copyStackToRun copies content/terraform/stacks/<target>/ + modules/
// into runs/<id>/terraform/<target>/ so terraform.exe sees a stable working
// dir whose state we own per run. Returns the destination stack dir.
func (o *Orchestrator) copyStackToRun() (string, error) {
	dstStack := o.tfStackDir()
	srcStack := filepath.Join(o.ContentDir, "terraform", "stacks", o.Inventory.Target.Type)
	if err := copyDir(srcStack, dstStack); err != nil {
		return "", fmt.Errorf("copy tf stack: %w", err)
	}
	// Copy modules alongside (relative paths in stacks/*/main.tf use ../../modules/).
	srcMods := filepath.Join(o.ContentDir, "terraform", "modules")
	dstMods := filepath.Join(o.runDir(), "terraform", "modules")
	if err := copyDir(srcMods, dstMods); err != nil {
		return "", fmt.Errorf("copy tf modules: %w", err)
	}
	return dstStack, nil
}

func (o *Orchestrator) sshKeyPath() string {
	if o.Inventory.Target.SSHKey != "" {
		return os.ExpandEnv(o.Inventory.Target.SSHKey)
	}
	// Default: ~/.ssh/id_ed25519
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "id_ed25519")
}

// copyDir does a recursive directory copy. Plain enough that pulling in
// a third-party dep would be overkill.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// ---- helpers ---------------------------------------------------------

func (o *Orchestrator) markStage(s state.Stage) {
	if err := o.Store.Update(o.Run.ID, func(r *state.Run) { r.Stage = s }); err != nil {
		o.Log.Error("orchestrator.markStage", "err", err)
	}
	o.emit("run:stage", string(s))
}

func (o *Orchestrator) fail(s state.Stage, err error) error {
	o.Log.Error("orchestrator.fail", "stage", string(s), "err", err)
	if updErr := o.Store.Update(o.Run.ID, func(r *state.Run) {
		r.Stage = state.StageFailed
		r.LastError = fmt.Sprintf("%s: %s", s, err)
	}); updErr != nil {
		o.Log.Error("orchestrator.fail.update", "err", updErr)
	}
	o.emit("run:stage", string(state.StageFailed), err.Error())
	return err
}

func (o *Orchestrator) emit(name string, data ...interface{}) {
	if o.Events != nil {
		o.Events.Emit(name, data...)
	}
}
