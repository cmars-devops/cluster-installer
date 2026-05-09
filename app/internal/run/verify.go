// Verify stage for the dev-vm topology. Runs four sanity checks over SSH
// against the freshly-installed VM and persists the per-check result on
// the Run so the wizard's Step 6 / Step 7 can render PASS/FAIL with the
// actual command output.
//
// Why these four:
//   1. SSH + os-release           — the VM is reachable and is the OS we asked for.
//   2. hostname / IP / MAC        — the autoinstall actually applied the inventory.
//   3. network + DNS              — default route + nameserver resolved a public name.
//   4. package manager            — the apt index refreshes (proves repo trust + DNS + network).
//
// These cover the complete "is the unattended install good?" question
// without diving into application-level concerns. Anything cluster-related
// (cephadm, k8s join) is beyond the dev-vm mode's scope.
package run

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/cmars-devops/cluster-installer/internal/inventory"
	"github.com/cmars-devops/cluster-installer/internal/runner/esxi"
	"github.com/cmars-devops/cluster-installer/internal/state"
)

// esxiClient is a thin var-binding so tests can swap the constructor
// out. Production code calls esxi.NewClient directly via this.
var esxiClient = esxi.NewClient

// runVerify is the orchestrator entry point for the StageVerify pipeline
// step (dev-vm topology only). Always operates on Inventory.Nodes[0] —
// schema enforces a single node in dev-vm mode.
func (o *Orchestrator) runVerify(ctx context.Context) error {
	if len(o.Inventory.Nodes) != 1 {
		return fmt.Errorf("dev-vm verify expects exactly 1 node, got %d", len(o.Inventory.Nodes))
	}
	node := o.Inventory.Nodes[0]
	user := o.sshUserFor(node.OS)

	o.emit("run:line", fmt.Sprintf("verify: connecting to %s@%s", user, node.IP))
	cli, err := dialSSH(ctx, node.IP, user, o.sshKeyPath())
	if err != nil {
		return fmt.Errorf("verify ssh: %w", err)
	}
	defer cli.Close()

	checks := []func(*ssh.Client, inventory.NodeSpec) state.VerifyCheck{
		checkOSRelease,
		checkHostnameIPMAC,
		checkNetworkDNS,
		checkPackageManager,
	}
	results := make([]state.VerifyCheck, 0, len(checks))
	allPass := true
	for _, fn := range checks {
		r := fn(cli, node)
		results = append(results, r)
		o.emitVerifyResult(r)
		if !r.Pass {
			allPass = false
		}
	}

	if err := o.Store.Update(o.Run.ID, func(r *state.Run) {
		r.VerifyResults = results
	}); err != nil {
		return fmt.Errorf("persist verify results: %w", err)
	}

	if !allPass {
		return fmt.Errorf("verify: one or more checks failed (see step 6 panel for details)")
	}

	// Post-verify cleanup — eject install ISO/CD-ROM devices AND wipe
	// the per-run upload tree from the ISO datastore. The install media
	// is single-use; keeping it around just leaves stale hardware on
	// the VM and burns datastore space.
	//
	// Both run inline (not deferred to app shutdown) so the operator
	// sees the cleanup happen before they switch away from the wizard,
	// and so cleanup runs even when the user later force-kills the app
	// instead of clicking the close button. Errors are logged but never
	// propagate — verify already passed, the install is good, no point
	// rolling that back over a housekeeping hiccup.
	if o.Inventory.Target.Type == "esxi" {
		o.postInstallESXiCleanup(ctx)
	}
	return nil
}

// postInstallESXiCleanup runs both ESXi cleanup steps under a single
// logged-in govmomi session: CD/DVD device removal (with a graceful
// shutdown + power-on cycle, since vSphere blocks CD-ROM hot-remove
// on a running VM) + datastore ISO wipe. They share one auth round-
// trip and one set of "starting" / "done" emits so the live log reads
// as one coherent step.
//
// "Where are the ISO files?" — we don't guess. Before detaching the
// CDs we read each VM's CD-ROM backings; vSphere itself tells us the
// exact "[datastore] path/file.iso" that's mounted. Then we delete
// THOSE paths verbatim. No more name-lookup mismatches.
//
// Total wall-clock cost: ~30-90 s (graceful shutdown + reconfigure +
// power on + datastore wipe).
func (o *Orchestrator) postInstallESXiCleanup(ctx context.Context) {
	o.emit("run:line", "→ post-install cleanup: VM 일시 셧다운 → CD 제거 → 재시작 → ISO 삭제 (약 30-90초)")

	c, err := esxiClient(ctx, o.Inventory.Target)
	if err != nil {
		o.emit("run:line", fmt.Sprintf("→ skip cleanup (ESXi connect failed: %v)", err))
		return
	}
	defer c.Close(ctx)
	emit := func(line string) { o.emit("run:line", line) }

	// 1. SOURCE OF TRUTH — read the ISO paths each VM's CD-ROM is
	//    bound to BEFORE we detach. This gives us the literal
	//    "[datastore] cluster-installer/<id>/file.iso" string vSphere
	//    is currently using, so the subsequent DeleteFile calls
	//    target the exact files (no name guessing).
	var sources []esxi.CDROMSource
	for _, n := range o.Inventory.Nodes {
		vmName := n.DisplayName
		if vmName == "" {
			vmName = n.Hostname
		}
		got, err := c.CollectCDROMSources(ctx, vmName)
		if err != nil {
			o.Log.Warn("postcleanup.collect_cds", "vm", vmName, "err", err)
			emit(fmt.Sprintf("cleanup: collect CD sources for %s — %v", vmName, err))
			continue
		}
		for _, s := range got {
			emit(fmt.Sprintf("cleanup: %s mounts %s", vmName, s.FullPath))
		}
		sources = append(sources, got...)
	}
	o.Log.Info("postcleanup.sources", "count", len(sources))

	// 2. Detach + power cycle. Removes the CD/DVD devices entirely so
	//    vSphere releases the host-side ISO file locks.
	for _, n := range o.Inventory.Nodes {
		vmName := n.DisplayName
		if vmName == "" {
			vmName = n.Hostname
		}
		if err := c.DetachAllCDROMs(ctx, vmName, emit); err != nil {
			o.Log.Warn("cd_eject", "vm", vmName, "err", err)
			emit(fmt.Sprintf("eject CD: error on %s: %v", vmName, err))
		}
	}

	// vSphere needs a moment after Reconfigure(DeviceChange) for the
	// VMFS file lock to clear. 2s is plenty in practice.
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return
	}

	// 3. Delete each ISO at its actual path. govmomi first; SSH
	//    fallback per-path on failure. Track parent directories so
	//    we can rmdir them after the files are gone.
	parentDirs := map[string]bool{} // "[datastore] cluster-installer/<id>" → true
	parentRels := map[string]string{} // ds-rel parent → datastore name (for SSH fallback)
	deleted, failed := 0, 0
	for _, s := range sources {
		if err := c.DeleteFileAtPath(ctx, s.FullPath); err != nil {
			emit(fmt.Sprintf("cleanup: govmomi delete %s — %v", s.FullPath, err))
			o.Log.Warn("postcleanup.file.govmomi", "path", s.FullPath, "err", err)
			// Per-file SSH fallback.
			if sshErr := sshDeleteSingleFile(ctx, o.Inventory.Target, s.DatastoreName, s.DsRelPath, emit); sshErr != nil {
				o.Log.Warn("postcleanup.file.ssh", "path", s.FullPath, "err", sshErr)
				failed++
			} else {
				o.Log.Info("postcleanup.file.ssh.ok", "path", s.FullPath)
				deleted++
			}
		} else {
			emit(fmt.Sprintf("cleanup: removed %s", s.FullPath))
			deleted++
		}
		if s.DatastoreName != "" && s.ParentDir != "" {
			parentDirs[fmt.Sprintf("[%s] %s", s.DatastoreName, s.ParentDir)] = true
			parentRels[s.ParentDir] = s.DatastoreName
		}
	}

	// 4. Remove the per-run parent directory(ies) once they're empty.
	for parentFull := range parentDirs {
		if err := c.DeleteFileAtPath(ctx, parentFull); err != nil {
			emit(fmt.Sprintf("cleanup: govmomi rmdir %s — %v", parentFull, err))
			o.Log.Warn("postcleanup.dir.govmomi", "path", parentFull, "err", err)
		} else {
			emit(fmt.Sprintf("cleanup: removed dir %s", parentFull))
		}
	}
	for rel, dsName := range parentRels {
		// Best-effort SSH rm -rf the parent — covers the case where
		// govmomi rmdir failed with a stale-lock or hidden-file
		// quirk. Silent success for already-gone dirs.
		_ = sshCleanupDatastorePath(ctx, o.Inventory.Target, dsName, rel, emit)
	}

	// 5. Try to remove the cluster-installer/ grandparent on each
	//    datastore we touched. govmomi rmdir + POSIX rmdir both refuse
	//    non-empty directories, so this is safe in the presence of
	//    parallel installer runs / unrelated content under the same
	//    prefix — the rmdir simply fails and we leave the dir alone.
	grandparentDS := map[string]bool{} // datastore name set
	for _, dsName := range parentRels {
		if dsName != "" {
			grandparentDS[dsName] = true
		}
	}
	for dsName := range grandparentDS {
		if err := c.TryRemoveEmptyDir(ctx, dsName, "cluster-installer"); err != nil {
			emit(fmt.Sprintf("cleanup: cluster-installer/ on [%s] kept (%v)", dsName, err))
			// SSH fallback: rmdir (POSIX) only succeeds on empty dirs,
			// matching govmomi's contract — safe if other run-ids still
			// live there.
			_ = sshExec(ctx, o.Inventory.Target,
				fmt.Sprintf("rmdir /vmfs/volumes/%s/cluster-installer 2>&1 || true && echo done", shellEscape(dsName)),
				emit)
		} else {
			emit(fmt.Sprintf("cleanup: removed cluster-installer/ on [%s]", dsName))
			o.Log.Info("postcleanup.parent_rmdir", "datastore", dsName)
		}
	}

	o.Log.Info("postcleanup.summary",
		"sources", len(sources),
		"deleted", deleted,
		"failed", failed,
		"dirs", len(parentDirs),
		"datastores", len(grandparentDS),
	)

	// Belt-and-suspenders: if no CD sources were captured (VM might
	// have been created on a build that already ejected, etc.), still
	// try the inventory-derived path as a last resort.
	if len(sources) == 0 {
		isoDS := o.Inventory.Target.ISODatastore
		if isoDS == "" {
			isoDS = o.Inventory.Target.Datastore
		}
		if isoDS == "" && len(o.Inventory.Nodes) > 0 {
			isoDS = o.Inventory.Nodes[0].Datastore
		}
		if isoDS != "" {
			dsRel := "cluster-installer/" + o.Run.ID
			emit(fmt.Sprintf("cleanup: no CD sources captured — trying inventory path [%s] %s", isoDS, dsRel))
			_ = sshCleanupDatastorePath(ctx, o.Inventory.Target, isoDS, dsRel, emit)
		}
	}

	o.emit("run:line", "→ post-install cleanup: done")
}

// sshDeleteSingleFile is the per-file SSH fallback when govmomi's
// DeleteFile returns an error. Wraps sshExec with a single
// `rm -f /vmfs/volumes/<ds>/<rel>` invocation.
func sshDeleteSingleFile(ctx context.Context, t inventory.TargetSpec, datastore, dsRel string, emit func(string)) error {
	if datastore == "" {
		return fmt.Errorf("no datastore name")
	}
	cmd := fmt.Sprintf("rm -f /vmfs/volumes/%s/%s && echo OK", shellEscape(datastore), shellEscape(dsRel))
	emit(fmt.Sprintf("SSH fallback (file): %s", cmd))
	return sshExec(ctx, t, cmd, emit)
}

// sshCleanupDatastorePath is the per-directory SSH fallback (rm -rf).
// Uses the operator-supplied ESXi root password.
func sshCleanupDatastorePath(ctx context.Context, t inventory.TargetSpec, datastore, dsRel string, emit func(string)) error {
	if datastore == "" {
		return fmt.Errorf("SSH fallback: no datastore name")
	}
	cmd := fmt.Sprintf("rm -rf /vmfs/volumes/%s/%s && echo OK", shellEscape(datastore), shellEscape(dsRel))
	emit(fmt.Sprintf("SSH fallback (dir): %s", cmd))
	return sshExec(ctx, t, cmd, emit)
}

// sshExec is the shared transport for the SSH fallbacks. Dials the ESXi
// host's :22, runs cmd, returns nil on success and the dial/handshake/
// command error otherwise. SSH must be enabled on the host (the hypervisor
// admin's call) and the Step 2 root password must be valid.
func sshExec(ctx context.Context, t inventory.TargetSpec, cmd string, emit func(string)) error {
	host := strings.TrimPrefix(strings.TrimPrefix(t.Endpoint, "https://"), "http://")
	if i := strings.IndexByte(host, '/'); i > 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	if host == "" || t.Password == "" {
		return fmt.Errorf("SSH fallback: missing host or password")
	}

	cfg := &ssh.ClientConfig{
		User:            stringOr(t.Username, "root"),
		Auth:            []ssh.AuthMethod{ssh.Password(t.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — same lab posture as the rest
		Timeout:         15 * time.Second,
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(host, "22"))
	if err != nil {
		emit(fmt.Sprintf("SSH fallback: dial %s — %v (ESXi SSH not enabled?)", host, err))
		return err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, host, cfg)
	if err != nil {
		_ = conn.Close()
		emit(fmt.Sprintf("SSH fallback: handshake — %v", err))
		return err
	}
	cli := ssh.NewClient(c, chans, reqs)
	defer cli.Close()

	sess, err := cli.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	out, err := sess.Output(cmd)
	if err != nil {
		emit(fmt.Sprintf("SSH fallback: %v output=%s", err, string(out)))
		return err
	}
	emit(fmt.Sprintf("SSH fallback: ok (%s)", strings.TrimSpace(string(out))))
	return nil
}

// shellEscape produces a value safe to interpolate into a single-quoted
// or unquoted POSIX shell argument. We expect datastore + run-id values
// — alphanumeric, dash, dot — but harden anyway.
func shellEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '/', r == '@':
			b.WriteRune(r)
		default:
			b.WriteRune('\\')
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stringOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// emitVerifyResult pushes a structured result event so the frontend can
// render a per-check row without parsing log lines.
func (o *Orchestrator) emitVerifyResult(r state.VerifyCheck) {
	o.emit("run:verify", map[string]any{
		"id":     r.ID,
		"label":  r.Label,
		"pass":   r.Pass,
		"detail": r.Detail,
	})
	tag := "PASS"
	if !r.Pass {
		tag = "FAIL"
	}
	o.emit("run:line", fmt.Sprintf("verify %-22s %s", r.ID, tag))
}

// ── Check #1: SSH reachable + /etc/os-release matches inventory ─────────

func checkOSRelease(cli *ssh.Client, n inventory.NodeSpec) state.VerifyCheck {
	out, err := runSSH(cli, "cat /etc/os-release")
	if err != nil {
		return failCheck("ssh_os_release", "SSH + os-release", err.Error())
	}
	got := parseOSRelease(out)
	want := osIDFor(n.OS)
	if want != "" && got["ID"] != want {
		return failCheck("ssh_os_release", "SSH + os-release",
			fmt.Sprintf("expected ID=%s, got ID=%s\n---\n%s", want, got["ID"], out))
	}
	return state.VerifyCheck{
		ID:     "ssh_os_release",
		Label:  "SSH + os-release",
		Pass:   true,
		Detail: fmt.Sprintf("ID=%s VERSION_ID=%s", got["ID"], got["VERSION_ID"]),
	}
}

// osIDFor returns the /etc/os-release ID= value our seeds produce for a
// given inventory OS family. Empty string means "don't enforce" — useful
// for OSes whose ID may legitimately vary across point releases.
func osIDFor(os string) string {
	switch os {
	case "ubuntu":
		return "ubuntu"
	case "leap":
		return "opensuse-leap"
	case "tumbleweed":
		return "opensuse-tumbleweed"
	case "microos":
		return "opensuse-microos"
	}
	return ""
}

func parseOSRelease(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

// ── Check #2: hostname / IP / MAC match inventory ──────────────────────

func checkHostnameIPMAC(cli *ssh.Client, n inventory.NodeSpec) state.VerifyCheck {
	host, err := runSSH(cli, "hostnamectl --static")
	if err != nil {
		return failCheck("hostname_ip_mac", "hostname/IP/MAC", "hostnamectl: "+err.Error())
	}
	host = strings.TrimSpace(host)
	if host != n.Hostname {
		return failCheck("hostname_ip_mac", "hostname/IP/MAC",
			fmt.Sprintf("hostname mismatch: expected %s, got %s", n.Hostname, host))
	}

	ipJSON, err := runSSH(cli, "ip -j -br addr show")
	if err != nil {
		return failCheck("hostname_ip_mac", "hostname/IP/MAC", "ip addr: "+err.Error())
	}
	type ifaddr struct {
		IFName string   `json:"ifname"`
		Addr   []string `json:"addr_info"`
	}
	// `ip -j -br addr show` returns array of objects with addr_info as
	// an array of {family, local, prefixlen}; `-br` flattens it. To stay
	// resilient, just substring-check the JSON for the inventory IP.
	if !strings.Contains(ipJSON, n.IP) {
		return failCheck("hostname_ip_mac", "hostname/IP/MAC",
			fmt.Sprintf("inventory IP %s not present in `ip addr` output:\n%s", n.IP, ipJSON))
	}

	if n.PrimaryMAC != "" {
		linkJSON, err := runSSH(cli, "ip -j -br link show")
		if err != nil {
			return failCheck("hostname_ip_mac", "hostname/IP/MAC", "ip link: "+err.Error())
		}
		if !strings.Contains(strings.ToLower(linkJSON), strings.ToLower(n.PrimaryMAC)) {
			return failCheck("hostname_ip_mac", "hostname/IP/MAC",
				fmt.Sprintf("expected MAC %s not found in `ip link` output:\n%s", n.PrimaryMAC, linkJSON))
		}
	}
	return state.VerifyCheck{
		ID:     "hostname_ip_mac",
		Label:  "hostname/IP/MAC",
		Pass:   true,
		Detail: fmt.Sprintf("hostname=%s ip=%s mac=%s", host, n.IP, n.PrimaryMAC),
	}
}

// ── Check #3: default route + DNS resolves ──────────────────────────────

func checkNetworkDNS(cli *ssh.Client, n inventory.NodeSpec) state.VerifyCheck {
	route, err := runSSH(cli, "ip route show default")
	if err != nil {
		return failCheck("network_dns", "network + DNS", "ip route: "+err.Error())
	}
	if !strings.Contains(route, "default") {
		return failCheck("network_dns", "network + DNS",
			"no default route\n---\n"+route)
	}

	// Pick a probe domain whose resolvability tells us DNS works AND
	// the OS-specific repo network path is reachable.
	probe := "download.ubuntu.com"
	if n.OS == "leap" || n.OS == "microos" || n.OS == "tumbleweed" {
		probe = "download.opensuse.org"
	}
	out, err := runSSH(cli, "getent hosts "+probe)
	if err != nil {
		return failCheck("network_dns", "network + DNS",
			fmt.Sprintf("getent hosts %s: %s", probe, err.Error()))
	}
	if strings.TrimSpace(out) == "" {
		return failCheck("network_dns", "network + DNS",
			"DNS resolution returned empty for "+probe)
	}
	return state.VerifyCheck{
		ID:    "network_dns",
		Label: "network + DNS",
		Pass:  true,
		Detail: fmt.Sprintf("default route present; %s resolved to: %s",
			probe, strings.TrimSpace(strings.Split(out, "\n")[0])),
	}
}

// ── Check #4: package manager refresh works ─────────────────────────────

func checkPackageManager(cli *ssh.Client, n inventory.NodeSpec) state.VerifyCheck {
	cmd := pkgRefreshCmd(n.OS)
	if cmd == "" {
		return state.VerifyCheck{
			ID:     "package_manager",
			Label:  "package manager",
			Pass:   true,
			Detail: "skipped (no probe for OS=" + n.OS + ")",
		}
	}
	out, err := runSSH(cli, cmd)
	if err != nil {
		return failCheck("package_manager", "package manager",
			fmt.Sprintf("%s: %s\n---\n%s", cmd, err.Error(), out))
	}
	return state.VerifyCheck{
		ID:     "package_manager",
		Label:  "package manager",
		Pass:   true,
		Detail: cmd + " ok",
	}
}

func pkgRefreshCmd(os string) string {
	switch os {
	case "ubuntu":
		// -qq + -o=… silences the progress bar. apt update returns 0
		// with non-zero exit only on hard failures (network, GPG).
		return "sudo apt-get update -qq -o=Dpkg::Use-Pty=0"
	case "leap", "tumbleweed":
		return "sudo zypper -n --no-cd refresh"
	case "microos":
		// MicroOS is read-only; transactional-update is the right
		// surface but invoking it for a one-shot check is heavyweight.
		// `transactional-update -d cleanup` is a no-op probe that
		// still exercises the snapshot machinery.
		return "sudo transactional-update -d cleanup"
	}
	return ""
}

// ── helpers ─────────────────────────────────────────────────────────────

func failCheck(id, label, detail string) state.VerifyCheck {
	return state.VerifyCheck{ID: id, Label: label, Pass: false, Detail: detail}
}

func dialSSH(ctx context.Context, host, user, keyPath string) (*ssh.Client, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — fresh-install hosts have no known key
		Timeout:         15 * time.Second,
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), 10*time.Second)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, host, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), ctx.Err()
}

// runSSH runs cmd on cli and returns combined stdout+stderr. Sessions
// are per-command — opening a fresh session is cheap and lets each
// check own its lifecycle.
//
// Why two buffers (stdout + stderr) instead of one shared one:
// golang.org/x/crypto/ssh writes stdout and stderr from separate
// goroutines, and bytes.Buffer is NOT goroutine-safe. Pointing both
// streams at the same buffer is a textbook data race: in the field we
// observed every command returning empty stdout (run.json verify
// detail "got ID=" / "ip addr output:" / "no default route") even
// though the same command run interactively works fine. Two buffers,
// joined after Run, fixes it without losing stderr context for failed
// commands.
func runSSH(cli *ssh.Client, cmd string) (string, error) {
	sess, err := cli.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	runErr := sess.Run(cmd)
	out := stdout.String()
	if e := stderr.String(); e != "" {
		// Stderr appended only when present, so the common path stays
		// clean. Visible in run.json verify detail when something went
		// wrong.
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += e
	}
	return out, runErr
}

