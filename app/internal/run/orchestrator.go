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

	"github.com/triangles-co-kr/cluster-installer/internal/httpserve"
	"github.com/triangles-co-kr/cluster-installer/internal/inventory"
	"github.com/triangles-co-kr/cluster-installer/internal/logging"
	"github.com/triangles-co-kr/cluster-installer/internal/netutil"
	apruntime "github.com/triangles-co-kr/cluster-installer/internal/runtime"
	"github.com/triangles-co-kr/cluster-installer/internal/seed"
	"github.com/triangles-co-kr/cluster-installer/internal/state"
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
// and aborts at the next stage boundary.
func (o *Orchestrator) Apply(ctx context.Context) error {
	defer func() {
		if o.httpSrv != nil {
			_ = o.httpSrv.Stop()
			o.emit("run:stage", string(state.StageCompleted), "http server stopped")
		}
	}()

	stages := []struct {
		name state.Stage
		fn   func(context.Context) error
	}{
		{state.StageSeedISO, o.startHTTPAndRenderSeeds},
		{state.StageTFInit, o.terraformInit},
		{state.StageTFPlan, o.terraformPlan},
		{state.StageTFApply, o.terraformApply},
		{state.StageWaitSSH, o.waitSSH},
		{state.StagePreflight, func(c context.Context) error { return o.runPlaybook(c, "playbooks/00-preflight.yml") }},
		{state.StageCeph, func(c context.Context) error { return o.runPlaybook(c, "playbooks/10-ceph-cephadm.yml") }},
		{state.StageK8s, o.runK8sPlaybook},
		{state.StageCSI, func(c context.Context) error { return o.runPlaybook(c, "playbooks/30-csi-ceph.yml") }},
		{state.StageAddons, func(c context.Context) error { return o.runPlaybook(c, "playbooks/40-addons.yml") }},
	}

	for _, st := range stages {
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

// ---- stub stages — Phase 1 implementation lands these ----------------

func (o *Orchestrator) terraformInit(ctx context.Context) error {
	return o.todo("terraform init")
}
func (o *Orchestrator) terraformPlan(ctx context.Context) error {
	return o.todo("terraform plan")
}
func (o *Orchestrator) terraformApply(ctx context.Context) error {
	return o.todo("terraform apply (must inject inst.auto=" + o.baseURL + "/profiles/<host>.json into VM kernel cmdline)")
}
func (o *Orchestrator) waitSSH(ctx context.Context) error {
	return o.todo("ssh wait on all nodes")
}
func (o *Orchestrator) runK8sPlaybook(ctx context.Context) error {
	pb := "playbooks/20-rke2.yml"
	if o.Inventory.Cluster.Kubernetes.Distro == "k3s" {
		pb = "playbooks/20-k3s.yml"
	}
	return o.runPlaybook(ctx, pb)
}
func (o *Orchestrator) runPlaybook(ctx context.Context, rel string) error {
	return o.todo("ansible-playbook " + rel)
}

func (o *Orchestrator) todo(what string) error {
	o.Log.Info("orchestrator.todo", "stage", what)
	o.emit("run:line", "[TODO] "+what)
	return nil
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
