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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	}
	// ESXi has no equivalent of libvirt's "let qemu read /tmp/seed-foo.iso"
	// — every CD-ROM backing must already be on a vSphere datastore. So
	// we slot in an ESXi-only upload stage between seed render and TF
	// init. Other targets get a strikethrough on that pill so users see
	// it's intentionally bypassed instead of stuck.
	if o.Inventory.Target.Type == "esxi" {
		common = append(common, stage{state.StageDSUpload, o.uploadSeedsToDatastore})
	} else {
		o.skip(state.StageDSUpload, "target!=esxi")
	}
	// Preflight is NOT in `common` because dev-vm topology bypasses
	// Ansible entirely. Cluster topologies append it explicitly below.
	common = append(common,
		stage{state.StageTFInit, o.terraformInit},
		stage{state.StageTFPlan, o.terraformPlan},
		stage{state.StageTFApply, o.terraformApply},
		stage{state.StageWaitSSH, o.waitSSH},
	)
	preflightStage := stage{state.StagePreflight, func(c context.Context) error {
		return o.runPlaybook(c, "playbooks/00-preflight.yml")
	}}

	cephStage := stage{state.StageCeph, func(c context.Context) error { return o.runPlaybook(c, "playbooks/10-ceph-cephadm.yml") }}
	k8sStage := stage{state.StageK8s, o.runK8sPlaybook}
	csiStage := stage{state.StageCSI, func(c context.Context) error { return o.runPlaybook(c, "playbooks/30-csi-ceph.yml") }}
	addonsStage := stage{state.StageAddons, func(c context.Context) error { return o.runPlaybook(c, "playbooks/40-addons.yml") }}

	switch topo {
	case inventory.TopologyDevVM:
		// Single-VM unattended-install verification mode. Skip every
		// cluster-level stage and replace preflight with a verify stage
		// that proves the OS install actually worked (SSH, hostname,
		// IP/MAC, network/DNS, package manager) — see run/verify.go.
		o.skip(state.StagePreflight, "topology=dev-vm")
		o.skip(state.StageCeph, "topology=dev-vm")
		o.skip(state.StageK8s, "topology=dev-vm")
		o.skip(state.StageCSI, "topology=dev-vm")
		o.skip(state.StageAddons, "topology=dev-vm")
		return append(common, stage{state.StageVerify, o.runVerify})

	case "ceph-only":
		o.skip(state.StageVerify, "topology=ceph-only (dev-vm only)")
		o.skip(state.StageK8s, "topology=ceph-only")
		o.skip(state.StageCSI, "topology=ceph-only")
		o.skip(state.StageAddons, "topology=ceph-only")
		return append(common, preflightStage, cephStage)

	case "k8s-only":
		o.skip(state.StageVerify, "topology=k8s-only (dev-vm only)")
		o.skip(state.StageCeph, "topology=k8s-only")
		out := append(common, preflightStage, k8sStage)
		if o.Inventory.Cluster.ExternalCeph != nil {
			out = append(out, csiStage)
		} else {
			o.skip(state.StageCSI, "topology=k8s-only without external_ceph")
		}
		return append(out, addonsStage)

	default: // "combined"
		o.skip(state.StageVerify, "topology=combined (dev-vm only)")
		return append(common, preflightStage, cephStage, k8sStage, csiStage, addonsStage)
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
	// 1. Pick advertise IP — the Windows NIC reachable FROM the VMs, not
	// the one reachable to the hypervisor management endpoint. Multi-homed
	// hosts (typical: laptop on Wi-Fi for ESXi mgmt + LAN for VM network)
	// otherwise advertise the wrong NIC and VMs hang at cloud-init network
	// stage trying to fetch user-data over an unreachable subnet.
	//
	// Order:
	//   1. target.advertise_ip (manual override, when set in Step 2)
	//   2. UDP-dial the VM network gateway — the NIC routing there is the
	//      one VMs will use to reach us back
	//   3. fallback: hypervisor endpoint (preserved for legacy/single-NIC)
	var hostIP string
	if ip := o.Inventory.Target.AdvertiseIP; ip != "" {
		hostIP = ip
	} else if gw := o.Inventory.Network.Gateway; gw != "" {
		ip, err := netutil.PickAdvertiseIP(gw)
		if err != nil {
			return fmt.Errorf("pick advertise IP for VM gateway %s: %w", gw, err)
		}
		hostIP = ip
	} else {
		ip, err := netutil.PickAdvertiseIP(o.Inventory.Target.Endpoint)
		if err != nil {
			return fmt.Errorf("pick advertise IP: %w", err)
		}
		hostIP = ip
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

	// 5. Materialise OS images into the cache + extract Agama kernel-boot
	// artefacts into staging/repo/. Skipped entirely if no Leap/Tumbleweed
	// node is present (MicroOS uses Combustion + the qcow2 base volume).
	if err := o.ensureAgamaArtefacts(ctx); err != nil {
		return fmt.Errorf("agama artefacts: %w", err)
	}

	// 6. Render seed payloads per node.
	hostsEntries := seed.HostsEntriesFromInventory(o.Inventory)
	for _, n := range o.Inventory.Nodes {
		sctx := seed.BuildContext(o.Inventory, n, o.Run.RootPasswordHash, hostsEntries)
		switch n.OS {
		case "leap":
			if err := o.renderAgama(sctx, n, "leap.auto.json.tmpl"); err != nil {
				return err
			}
		case "tumbleweed":
			if err := o.renderAgama(sctx, n, "tumbleweed.auto.json.tmpl"); err != nil {
				return err
			}
		case "microos":
			if err := o.renderCombustion(sctx, n); err != nil {
				return err
			}
		case "ubuntu":
			if err := o.renderAutoinstall(ctx, sctx, n); err != nil {
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

// renderAutoinstall builds a per-node "cidata" ISO holding user-data +
// meta-data for Ubuntu autoinstall. The big install ISO is remastered ONCE
// per OS family (see remasterUbuntuShared) and referenced by every Ubuntu
// VM; the small cidata ISO carries the only per-node payload (~64 KB).
//
// cloud-init's NoCloud datasource scans block devices for a CD with
// volume label "cidata" and reads user-data + meta-data straight off it
// — no HTTP fetch, no per-node remaster, no kernel-cmdline gymnastics.
//
// Also writes the same files under staging/profiles/<hostname>/ so the
// HTTP server can still serve them for diagnostic curl-from-VM workflows
// or for ds=nocloud-net debug paths; these are not the primary delivery
// mechanism.
func (o *Orchestrator) renderAutoinstall(rctx context.Context, sctx seed.Context, n inventory.NodeSpec) error {
	udTmpl := filepath.Join(o.ContentDir, "seeds", "autoinstall", "user-data.tmpl")
	userData, err := seed.RenderFile(udTmpl, sctx)
	if err != nil {
		return fmt.Errorf("render autoinstall user-data %s: %w", n.Hostname, err)
	}
	// Sudo username override: the content template hardcodes 'triangles'
	// in identity.username, sudoers.d/90-triangles, /home/triangles,
	// chpasswd, etc. When the operator picked a different username on
	// Step 1, swap every literal 'triangles' for the chosen value. Done
	// Go-side so the swap works with the existing content tag (no
	// content-repo update needed).
	if u := o.Inventory.ClusterAuth.SudoUser(); u != "" && u != "triangles" {
		userData = rewriteUserDataUsername(userData, u)
	}
	// Network block rewrite — MAC-based matching, always.
	// The content template hardcodes the NIC name (ens192) as the
	// netplan ethernets KEY. That's brittle: vSphere may attach a
	// different adapter (e1000e → ens33 in the field, vmxnet3 → ens192,
	// libvirt virtio → enp1s0, …) and a name mismatch silently drops
	// the static IP and falls back to DHCP. We always rewrite the
	// `network:` block to use `match: { macaddress: ... }` + `set-name:
	// ens192` so the rule pins to the deterministically-allocated MAC
	// regardless of NIC adapter type, and the installed system always
	// surfaces the interface as ens192. Static / DHCP both go through
	// the same path now; n.PrimaryMAC is pre-allocated at run start.
	if n.PrimaryMAC != "" {
		userData = rewriteUserDataNetwork(userData, n, sctx, o.Inventory.Target)
	}
	// dev-vm minimal install: the content template's `packages:` list
	// (chrony / lvm2 / nfs-common / open-iscsi / curl / jq) is for
	// CLUSTER nodes (cephadm + RKE2 prep). On a single independent VM
	// none of those are needed at install time — and any of them
	// failing to download from the apt mirror at install time aborts
	// the whole install with `curtin system-install ... exit 100`,
	// which we observed in the field. Strip the block so subiquity
	// installs just the base + openssh-server (ssh.install-server: true).
	// The operator can apt-install whatever they need post-install.
	if o.Inventory.Cluster.IsDevVM() {
		userData = stripUserDataPackages(userData)
	}
	mdTmpl := filepath.Join(o.ContentDir, "seeds", "autoinstall", "meta-data.tmpl")
	metaData, err := seed.RenderFile(mdTmpl, sctx)
	if err != nil {
		return fmt.Errorf("render autoinstall meta-data %s: %w", n.Hostname, err)
	}

	// HTTP-served copy (diagnostic / fallback only).
	dir := filepath.Join(o.stagingDir, "profiles", n.Hostname)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "user-data"), userData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta-data"), metaData, 0o644); err != nil {
		return err
	}

	// Per-node cidata ISO (the actual delivery channel). Built via pycdlib
	// because go-diskfs's iso9660 backend miscounts byte writes on small
	// files (consistent "copied N bytes, expected 0" failure observed on
	// 64-byte meta-data writes). pycdlib is already on hand for the Ubuntu
	// install-ISO remaster, so the dependency cost is zero.
	isoPath := filepath.Join(o.stagingDir, "seeds", "seed-"+n.Hostname+".iso")
	if err := o.buildCidataISO(rctx, dir, isoPath); err != nil {
		return fmt.Errorf("build cidata iso %s: %w", n.Hostname, err)
	}
	o.Log.Info("seed.autoinstall", "host", n.Hostname, "cidata", isoPath)
	return nil
}

// buildCidataISO shells out to content/seeds/autoinstall/build-cidata.py via
// the embedded uv runtime. The script reads user-data + meta-data from
// stagingProfileDir and writes a small ISO9660 image labeled "cidata" to
// outPath. cloud-init's NoCloud datasource scans connected block devices
// for that volume label and discovers the per-node payload automatically.
func (o *Orchestrator) buildCidataISO(ctx context.Context, stagingProfileDir, outPath string) error {
	uvPath := filepath.Join(apruntime.BinDir(), "uv.exe")
	if _, err := os.Stat(uvPath); err != nil {
		return fmt.Errorf("uv not extracted: %w", err)
	}
	scriptPath := filepath.Join(o.ContentDir, "seeds", "autoinstall", "build-cidata.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("build-cidata.py not found: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(outPath) // idempotent — clear leftover from a failed earlier run

	cmd := exec.CommandContext(ctx, uvPath,
		"run", "--quiet", "--with", "pycdlib",
		"python", scriptPath,
		"--user-data", filepath.Join(stagingProfileDir, "user-data"),
		"--meta-data", filepath.Join(stagingProfileDir, "meta-data"),
		"--out", outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pycdlib build-cidata: %w\noutput:\n%s", err, string(out))
	}
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
	o.emit("run:line", fmt.Sprintf("waiting for SSH on %d nodes (timeout 30m each)", len(o.Inventory.Nodes)))
	// Group hosts by SSH user. openSUSE seeds enable root login; Ubuntu
	// autoinstall disables root by default and creates the primary
	// 'ubuntu' user — wait for the right identity per node.
	byUser := map[string][]string{}
	for _, n := range o.Inventory.Nodes {
		u := o.sshUserFor(n.OS)
		byUser[u] = append(byUser[u], n.IP)
	}
	for user, hosts := range byUser {
		if err := runner.WaitForSSH(ctx, hosts, user, o.sshKeyPath(), 30*time.Minute); err != nil {
			return err
		}
	}
	return nil
}

// sshUserFor maps an OS family to the user account the OS-install seed
// authorised our SSH key against. For Ubuntu autoinstall the answer is
// the cluster-wide sudo user (Step 1 → "사용자명") which subiquity
// creates in late-commands; for openSUSE Combustion/Agama paths the
// seed authorises root directly. Centralised so verify.go and waitSSH
// stay in lockstep on which identity to use.
func (o *Orchestrator) sshUserFor(os string) string {
	switch os {
	case "ubuntu":
		return o.Inventory.ClusterAuth.SudoUser()
	default:
		return "root"
	}
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
	// Keep the same `stacks/<target>/` layout as content/terraform/ so
	// the relative `source = "../../modules/<name>"` references in
	// stack main.tf files resolve correctly after copying.
	return filepath.Join(o.runDir(), "terraform", "stacks", o.Inventory.Target.Type)
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
	// Distinguish user-cancellation from genuine errors so the UI can offer
	// "Resume" instead of "Retry" and the log doesn't read like a crash.
	cancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)

	if cancelled {
		o.Log.Info("orchestrator.cancelled", "stage", string(s))
	} else {
		o.Log.Error("orchestrator.fail", "stage", string(s), "err", err)
	}

	if updErr := o.Store.Update(o.Run.ID, func(r *state.Run) {
		r.Stage = state.StageFailed
		if cancelled {
			r.LastError = fmt.Sprintf("%s: cancelled by user", s)
		} else {
			r.LastError = fmt.Sprintf("%s: %s", s, err)
		}
	}); updErr != nil {
		o.Log.Error("orchestrator.fail.update", "err", updErr)
	}

	if cancelled {
		o.emit("run:stage", string(state.StageFailed), "cancelled by user")
	} else {
		o.emit("run:stage", string(state.StageFailed), err.Error())
	}
	return err
}

func (o *Orchestrator) emit(name string, data ...interface{}) {
	if o.Events != nil {
		o.Events.Emit(name, data...)
	}
}

// stripUserDataPackages removes the `packages:` block from a rendered
// subiquity user-data document. The block is YAML — a top-level
// (autoinstall-level, 2-space indented) `packages:` key followed by N
// list entries (`    - <name>`). The block ends at the next sibling
// 2-space-indented key (e.g. `late-commands:` or `shutdown:`) or EOF.
//
// Why: the content template's package list is cluster-prep (cephadm,
// nfs-common, open-iscsi, jq, …) and is wrong for the single-VM dev-vm
// flow. Worse, if any one of those packages can't download at install
// time (transient mirror outage, partial DHCP, GeoIP redirect to a slow
// mirror, …) subiquity aborts the whole install. Dropping the block
// keeps the install minimal and resilient — the operator apt-installs
// what they want post-boot.
func stripUserDataPackages(userData []byte) []byte {
	const blockKey = "  packages:" // 2-space indent, autoinstall child
	lines := bytes.Split(userData, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	skipping := false
	for _, line := range lines {
		if !skipping && bytes.Equal(bytes.TrimRight(line, " \t"), []byte(blockKey)) {
			// Drop the `  packages:` header and start skipping its
			// continuation lines (deeper indent).
			skipping = true
			continue
		}
		if skipping {
			// A line with indent ≤ 2 spaces and a non-space at column 2
			// is the next sibling key — block ends, resume copying. An
			// empty line counts as part of the block's whitespace.
			if len(line) == 0 {
				continue
			}
			if line[0] != ' ' || (len(line) >= 3 && line[2] != ' ') {
				skipping = false
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

// rewriteUserDataUsername replaces every literal occurrence of the default
// 'triangles' sudo username in a rendered subiquity user-data document
// with the operator-chosen value. The content template hardcodes the
// name in seven places (identity.username, sudoers entry filename + body,
// /home/triangles paths, -o/-g triangles owner, sudo -u triangles in
// ssh-import-id-gh, chpasswd target). They're all the same token, so a
// global string replace is safe — there's no other context where
// 'triangles' would legitimately appear in this template.
func rewriteUserDataUsername(userData []byte, newUser string) []byte {
	return bytes.ReplaceAll(userData, []byte("triangles"), []byte(newUser))
}

// rewriteUserDataNetwork replaces the entire `  network:` ... `<next>:`
// block in a rendered subiquity user-data document with a freshly-built
// netplan block that pins to NodeSpec.PrimaryMAC. This makes the network
// config robust to vSphere attaching the NIC under a different name than
// the content template assumes (the field-failure mode: template ships
// ens192, ESXi attaches e1000e → ens33, netplan apply silently no-ops
// the static config, VM ends up on DHCP).
//
// Output shape (static):
//
//	  network:
//	    version: 2
//	    ethernets:
//	      primary:
//	        match:
//	          macaddress: "00:50:56:..."
//	        set-name: ens192
//	        addresses:
//	          - "10.0.0.50/24"
//	        routes:
//	          - to: default
//	            via: 10.0.0.1
//	        nameservers:
//	          addresses: ["1.1.1.1", "8.8.8.8"]
//
// Output shape (dhcp):
//
//	  network:
//	    version: 2
//	    ethernets:
//	      primary:
//	        match:
//	          macaddress: "00:50:56:..."
//	        set-name: ens192
//	        dhcp4: true
//
// The `set-name: ens192` line normalises the in-OS NIC name regardless
// of which adapter type vSphere attached, so verify / docs / scripts can
// keep referring to ens192 consistently.
func rewriteUserDataNetwork(userData []byte, n inventory.NodeSpec, sctx seed.Context, target inventory.TargetSpec) []byte {
	iface := sctx.Node.NetworkInterface
	if iface == "" {
		iface = "ens192"
	}

	// Build effective NIC list. nics[0] always carries the primary
	// gateway/nameservers (Network*Spec values are the cluster-wide
	// defaults that apply to NIC[0] when the operator left per-NIC
	// fields blank). Extra NICs only get static IP/prefix; the operator
	// configures their routing rules in the guest if needed.
	//
	// Pass the actual port-group names so the synthesized list matches
	// what Terraform attaches at the VM level — without target's
	// clusterNetwork the synthesizer suppresses the cluster_ip NIC,
	// which would render a netplan that doesn't match the guest's
	// hardware (a NIC the autoinstall doesn't know about).
	effNICs := n.EffectiveNICs(target.Network, target.ClusterNetwork)

	var b bytes.Buffer
	b.WriteString("  network:\n")
	b.WriteString("    version: 2\n")
	b.WriteString("    ethernets:\n")
	for i, nic := range effNICs {
		key := fmt.Sprintf("nic%d", i)
		if i == 0 {
			key = "primary"
		}
		// Each NIC gets its own set-name. NIC[0] keeps the canonical
		// "ens192" so existing scripts/docs still work; subsequent
		// NICs get "ens193", "ens194", … so the OS surface is
		// predictable regardless of vSphere attach order.
		setName := iface
		if i > 0 {
			setName = fmt.Sprintf("ens%d", 192+i)
		}
		mac := nic.MAC
		if mac == "" && i == 0 {
			mac = n.PrimaryMAC
		}
		mode := nic.IPMode
		if mode == "" && i == 0 {
			mode = n.IPMode
		}
		ip := nic.IP
		if ip == "" && i == 0 {
			ip = n.IP
		}
		prefix := nic.PrefixLen
		if prefix == 0 {
			prefix = sctx.Network.PrefixLen
		}
		gw := nic.Gateway
		if gw == "" && i == 0 {
			gw = sctx.Network.Gateway
		}
		ns := nic.Nameservers
		if len(ns) == 0 && i == 0 {
			ns = sctx.Network.Nameservers
		}

		b.WriteString(fmt.Sprintf("      %s:\n", key))
		b.WriteString("        match:\n")
		b.WriteString(fmt.Sprintf("          macaddress: \"%s\"\n", mac))
		b.WriteString(fmt.Sprintf("        set-name: %s\n", setName))
		// Extra NICs MUST be marked optional. Otherwise
		// systemd-networkd-wait-online (which gates cloud-init's
		// network stage AND boot in general) blocks indefinitely
		// when ANY configured interface fails to come online —
		// extremely common for a 2nd NIC on a port-group that has
		// no DHCP server. The primary NIC stays mandatory so a
		// genuine misconfiguration there fails fast and visibly
		// instead of silently completing install with no reachable
		// SSH (verify dials primary NIC's IP, so a half-up primary
		// would just shift the failure mode further along).
		if i > 0 {
			b.WriteString("        optional: true\n")
		}
		if mode == "dhcp" {
			b.WriteString("        dhcp4: true\n")
			// Don't let a 2nd NIC's DHCP-supplied default route
			// override the primary's. Only relevant on extras.
			if i > 0 {
				b.WriteString("        dhcp4-overrides:\n")
				b.WriteString("          use-routes: false\n")
			}
			continue
		}
		// Static. Skip the block when no IP yet — netplan would error
		// on an empty addresses list.
		if ip == "" {
			b.WriteString("        dhcp4: false\n")
			continue
		}
		b.WriteString("        addresses:\n")
		b.WriteString(fmt.Sprintf("          - \"%s/%d\"\n", ip, defaultPrefixLen(prefix)))
		if gw != "" {
			b.WriteString("        routes:\n")
			b.WriteString("          - to: default\n")
			b.WriteString(fmt.Sprintf("            via: %s\n", gw))
		}
		if len(ns) > 0 {
			b.WriteString("        nameservers:\n")
			b.WriteString("          addresses: [")
			for j, n := range ns {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(fmt.Sprintf("\"%s\"", n))
			}
			b.WriteString("]\n")
		}
	}
	newBlock := b.Bytes()

	// Splice: find `  network:` line, skip until next 2-space sibling
	// key, emit newBlock in place.
	const headerKey = "  network:" // 2-space indent, autoinstall child
	lines := bytes.Split(userData, []byte("\n"))
	out := make([][]byte, 0, len(lines)+8)
	state := 0 // 0=before, 1=inside-old-block, 2=after
	for _, line := range lines {
		switch state {
		case 0:
			if bytes.Equal(bytes.TrimRight(line, " \t"), []byte(headerKey)) {
				// Insert new block (sans trailing newline so Join
				// re-inserts one).
				out = append(out, bytes.TrimRight(newBlock, "\n"))
				state = 1
				continue
			}
			out = append(out, line)
		case 1:
			// Inside old block: anything indented > 2 spaces (i.e. line
			// starts with " " and column 2 is " ") is a continuation.
			// First line at indent ≤2 with non-space at col 2 is the
			// next sibling key — block ends.
			if len(line) == 0 {
				continue
			}
			if line[0] != ' ' || (len(line) >= 3 && line[2] != ' ') {
				out = append(out, line)
				state = 2
				continue
			}
			// continuation, skip
		case 2:
			out = append(out, line)
		}
	}
	if state == 0 {
		// No `  network:` header found — leave a visible warning rather
		// than silently shipping the wrong config.
		return append([]byte("# WARN: network rewrite did not find a `network:` block\n"), userData...)
	}
	return bytes.Join(out, []byte("\n"))
}

func defaultPrefixLen(p int) int {
	if p == 0 {
		return 24
	}
	return p
}
