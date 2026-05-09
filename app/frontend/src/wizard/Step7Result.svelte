<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';
  import { api, type Run, type VerifyCheck, type LeftoverISOs } from '../lib/api';
  import { onMount } from 'svelte';

  const inv = $derived($wizardStore.inventory);
  const topology = $derived(inv.cluster.topology);
  const devVMMode = $derived(topology === 'dev-vm');
  const devVMNode = $derived(devVMMode ? inv.nodes[0] : undefined);

  let copied = $state<string | null>(null);
  function copy(value: string, label: string) {
    navigator.clipboard?.writeText(value);
    copied = label;
    setTimeout(() => (copied = null), 1500);
  }

  // dev-vm: load the persisted Run so we can render verify_results from
  // the canonical source (the run:verify event stream is only delivered
  // to Step 6's mounted component; this view should work after a refresh).
  let run = $state<Run | null>(null);
  let redeploying = $state(false);
  let redeployErr = $state('');

  // Leftover ISO diagnostics — list every cluster-installer/<run>/
  // dir still on the ISO datastore. Lets the operator confirm
  // post-install cleanup actually emptied the staging tree (and bulk
  // wipe accumulated leftovers from older runs that pre-date the
  // cleanup fix).
  let leftovers = $state<LeftoverISOs | null>(null);
  let leftoversLoading = $state(false);
  let wiping = $state(false);
  let wipeLog = $state<string[]>([]);

  async function refreshLeftovers() {
    if (!devVMMode) return;
    leftoversLoading = true;
    try {
      leftovers = await api.listLeftoverISOs(inv.target);
    } catch (e) {
      leftovers = { datastore: '', entries: [], total_gb: 0, error: String(e) };
    } finally {
      leftoversLoading = false;
    }
  }

  async function wipeAll() {
    if (wiping) return;
    if (!confirm('cluster-installer/ 아래 모든 staging 디렉토리를 삭제합니다. 진행 중인 다른 run이 없는지 확인하셨나요?')) return;
    wiping = true; wipeLog = [];
    try {
      await api.wipeLeftoverISOs(inv.target);
    } catch (e) {
      wipeLog = [...wipeLog, 'ERROR: ' + e];
    } finally {
      wiping = false;
      await refreshLeftovers();
    }
  }

  onMount(async () => {
    if (!$wizardStore.runId) return;
    try { run = await api.getRun($wizardStore.runId); } catch { run = null; }
    if (devVMMode) {
      // Listen for cleanup progress (emitted by WipeLeftoverISOs).
      const r: any = (window as any).runtime;
      r?.EventsOn?.('cleanup:line', (l: string) => { wipeLog = [...wipeLog, l]; });
      // Auto-load once on mount so the panel shows current state.
      await refreshLeftovers();
    }
  });
  const verifyResults = $derived<VerifyCheck[]>(run?.verify_results ?? []);

  async function redeploy() {
    if (!$wizardStore.runId || redeploying) return;
    redeploying = true; redeployErr = '';
    try {
      await api.redeployDevVM($wizardStore.runId);
      // Re-fetch — Apply runs in the background; Step 6 events will
      // flow as it progresses. We optimistically reload the snapshot.
      run = await api.getRun($wizardStore.runId);
    } catch (e) {
      redeployErr = String(e);
    } finally {
      redeploying = false;
    }
  }

  // ── cluster mode placeholders (existing behaviour) ──────────────────
  const kubeAPI = $derived(`https://${inv.network.vip}:6443`);
  const cephDashboard = $derived(
    inv.nodes.find((n) => n.roles.includes('ceph-mon'))
      ? `https://${inv.nodes.find((n) => n.roles.includes('ceph-mon'))!.ip}:8443`
      : '(no ceph-mon node)'
  );
  const ingressURL = $derived(`https://${inv.network.ingress_lb_ip}/`);
</script>

<header class="step-header">
  <h2>{$_('step.7.title')}</h2>
  <p>{devVMMode ? $_('step7.devVMSubtitle') : $_('step.7.subtitle')}</p>
</header>

{#if devVMMode && devVMNode}
  <Section title={$_('step7.devVMSummary')}>
    <div class="dev-summary">
      <div class="metric">
        <span class="metric-label">{$_('step7.devVMHostname')}</span>
        <code>{devVMNode.hostname}</code>
      </div>
      <div class="metric">
        <span class="metric-label">{$_('step7.devVMIP')}</span>
        <code>{devVMNode.ip}</code>
      </div>
      <div class="metric">
        <span class="metric-label">{$_('step7.devVMOS')}</span>
        <code>{devVMNode.os} {devVMNode.os_version ?? ''}</code>
      </div>
      <div class="metric">
        <span class="metric-label">{$_('step7.devVMResources')}</span>
        <code>{devVMNode.cpu}c · {devVMNode.memory_gb}G · {devVMNode.disk_gb}G</code>
      </div>
    </div>
  </Section>

  <Section title={$_('step7.devVMSSH')}>
    {@const sshUser = devVMNode.os === 'ubuntu' ? (inv.cluster_auth?.username || 'triangles') : 'root'}
    {@const sshCmd = `ssh ${sshUser}@${devVMNode.ip || '<ip>'}`}
    <div class="row">
      <code class="ssh-cmd">{sshCmd}</code>
      <Button variant="ghost" onclick={() => copy(sshCmd, 'ssh')}>{$_('common.copy')}</Button>
      {#if copied === 'ssh'}<Badge tone="success">copied</Badge>{/if}
    </div>
  </Section>

  {#if verifyResults.length > 0}
    <Section title={$_('step7.devVMVerify')} subtitle={$_('step7.devVMVerifyHint')}>
      <ul class="verify-list">
        {#each verifyResults as v}
          <li class="verify-row" class:fail={!v.pass}>
            <span class="verify-mark">{v.pass ? '✓' : '✗'}</span>
            <span class="verify-label">{v.label}</span>
            <Badge tone={v.pass ? 'success' : 'danger'}>{v.pass ? 'PASS' : 'FAIL'}</Badge>
            {#if v.detail}
              <details class="verify-detail">
                <summary>{$_('step7.verifyDetail')}</summary>
                <pre>{v.detail}</pre>
              </details>
            {/if}
          </li>
        {/each}
      </ul>
    </Section>
  {/if}

  <Section title="Datastore 잔존 ISO" subtitle="cluster-installer/ 아래 남아 있는 staging 디렉토리. 'OUR' 표시는 이 wizard가 만든 run, 'UNKNOWN'은 다른 곳에서 만든 디렉토리 — 후자는 절대 건드리지 않음.">
    {@const ownedEntries = leftovers ? leftovers.entries.filter((e) => e.owned) : []}
    {@const unknownEntries = leftovers ? leftovers.entries.filter((e) => !e.owned) : []}

    <div class="row">
      <Button variant="secondary" disabled={leftoversLoading} onclick={refreshLeftovers}>
        {leftoversLoading ? '확인 중…' : '↻ 다시 조회'}
      </Button>
      {#if leftovers}
        {#if leftovers.error}
          <Badge tone="danger">{leftovers.error}</Badge>
        {:else if leftovers.entries.length === 0}
          <Badge tone="success">잔존 없음</Badge>
        {:else}
          <Badge tone="warn">OUR {ownedEntries.length} · UNKNOWN {unknownEntries.length} · {leftovers.total_gb.toFixed(2)} GB</Badge>
        {/if}
      {/if}
    </div>

    {#if leftovers && leftovers.entries.length > 0}
      <div class="leftover-list">
        {#each leftovers.entries as e}
          <details class="leftover-row" class:unknown={!e.owned}>
            <summary>
              <Badge tone={e.owned ? 'info' : 'neutral'}>{e.owned ? 'OUR' : 'UNKNOWN'}</Badge>
              <code class="leftover-name">{e.path}</code>
              <span class="leftover-size">
                {e.files.length} files · {(e.files.reduce((s, f) => s + f.size, 0) / (1024*1024*1024)).toFixed(2)} GB
              </span>
              {#if e.error}<Badge tone="danger">{e.error}</Badge>{/if}
            </summary>
            <ul class="leftover-files">
              {#each e.files as f}
                <li>
                  <code>{f.name}</code>
                  <span class="size">{(f.size / (1024*1024)).toFixed(0)} MB</span>
                </li>
              {/each}
            </ul>
          </details>
        {/each}
      </div>

      <div class="row" style="margin-top: 0.75rem;">
        <Button variant="danger" disabled={wiping || ownedEntries.length === 0} onclick={wipeAll}>
          {wiping
            ? '삭제 중…'
            : ownedEntries.length === 0
              ? '🗑 정리할 OUR 항목 없음'
              : '🗑 OUR ' + ownedEntries.length + '개만 정리'}
        </Button>
        <span class="muted">UNKNOWN 항목은 안전을 위해 자동 삭제 대상에서 제외됩니다 (병렬 설치 도구가 같은 prefix를 쓸 수 있음).</span>
      </div>

      {#if wipeLog.length > 0}
        <pre class="wipe-log">{wipeLog.join('\n')}</pre>
      {/if}
    {/if}
  </Section>

  <Section title={$_('step7.devVMRedeploy')} subtitle={$_('step7.devVMRedeployHint')}>
    <div class="row">
      <Button variant="primary" disabled={redeploying} onclick={redeploy}>
        {redeploying ? $_('common.loading') : '↻ ' + $_('step7.devVMRedeployBtn')}
      </Button>
      {#if redeployErr}<Badge tone="danger">{redeployErr}</Badge>{/if}
    </div>
    <p class="muted">{$_('step7.devVMRedeployNote')}</p>
  </Section>
{:else}

<Section title={$_('step7.kubeconfig')} subtitle={$_('step7.kubeconfigDesc')}>
  <p class="muted">
    %LOCALAPPDATA%\cluster-installer\runs\{$wizardStore.runId ?? '<run-id>'}\kubeconfig
  </p>
  <div class="row">
    <Button variant="primary">{$_('step7.downloadKubeconfig')}</Button>
    <Button variant="secondary" onclick={() => copy('export KUBECONFIG=%LOCALAPPDATA%\\cluster-installer\\runs\\' + $wizardStore.runId + '\\kubeconfig', 'env')}>
      {$_('common.copy')} env
    </Button>
    {#if copied === 'env'}<Badge tone="success">copied</Badge>{/if}
  </div>
</Section>

<Section title={$_('step7.endpoints')}>
  <div class="endpoints">
    <div class="endpoint">
      <span class="label">{$_('step7.kubeAPI')}</span>
      <code>{kubeAPI}</code>
      <Button variant="ghost" onclick={() => copy(kubeAPI, 'kubeAPI')}>{$_('common.copy')}</Button>
      {#if copied === 'kubeAPI'}<Badge tone="success">copied</Badge>{/if}
    </div>

    {#if cephDashboard.startsWith('https://')}
      <div class="endpoint">
        <span class="label">{$_('step7.cephDashboard')}</span>
        <code>{cephDashboard}</code>
        <Button variant="ghost" onclick={() => copy(cephDashboard, 'ceph')}>{$_('common.copy')}</Button>
        {#if copied === 'ceph'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}

    {#if inv.addons.ingress !== 'none'}
      <div class="endpoint">
        <span class="label">{$_('step7.ingress')}</span>
        <code>{ingressURL}</code>
        <Button variant="ghost" onclick={() => copy(ingressURL, 'ingress')}>{$_('common.copy')}</Button>
        {#if copied === 'ingress'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}

    {#if inv.addons.gitops === 'argocd'}
      <div class="endpoint">
        <span class="label">{$_('step7.argocd')}</span>
        <code>{ingressURL}argocd</code>
        <Button variant="ghost" onclick={() => copy(ingressURL + 'argocd', 'argo')}>{$_('common.copy')}</Button>
        {#if copied === 'argo'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}
  </div>
</Section>

<Section title={$_('step7.nextSteps')}>
  <ol class="next-steps">
    <li><code>kubectl get nodes</code></li>
    <li><code>ssh root@{inv.nodes.find((n) => n.roles.includes('ceph-mon'))?.ip ?? '<mon>'} ceph -s</code></li>
    {#if inv.addons.gitops === 'argocd'}
      <li>Argo CD에 첫 Application 등록</li>
    {/if}
  </ol>
</Section>
{/if}

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .row { display: flex; gap: 0.5rem; align-items: center; }
  .muted { color: #71717a; font-size: 0.8rem; font-family: ui-monospace, monospace;
           background: #0f0f12; padding: 0.4rem 0.6rem; border-radius: 4px; margin: 0 0 0.5rem; }
  .endpoints { display: flex; flex-direction: column; gap: 0.5rem; }
  .endpoint { display: grid; grid-template-columns: 12rem 1fr auto auto; gap: 0.5rem; align-items: center; }
  .endpoint .label { color: #a1a1aa; font-size: 0.85rem; }
  .endpoint code { background: #0f0f12; padding: 0.3rem 0.5rem; border-radius: 4px;
                   font-family: ui-monospace, monospace; font-size: 0.82rem; color: #93c5fd; }
  .next-steps { padding-left: 1.25rem; margin: 0; line-height: 1.9; font-size: 0.85rem; color: #d4d4d8; }
  .next-steps code { background: #0f0f12; padding: 0.15rem 0.4rem; border-radius: 3px;
                     font-family: ui-monospace, monospace; font-size: 0.8rem; color: #93c5fd; }

  .dev-summary { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.6rem; }
  @media (max-width: 800px) { .dev-summary { grid-template-columns: repeat(2, 1fr); } }
  .metric { display: flex; flex-direction: column; gap: 0.2rem;
            padding: 0.5rem 0.7rem; background: #0a0a0c; border: 1px solid #1e3a8a;
            border-radius: 5px; }
  .metric-label { font-size: 0.7rem; color: #71717a; text-transform: uppercase; letter-spacing: 0.05em; }
  .metric code { background: transparent; padding: 0; color: #93c5fd; font-size: 0.92rem; }
  .ssh-cmd { background: #0f0f12; padding: 0.4rem 0.7rem; border-radius: 4px;
             font-family: ui-monospace, monospace; font-size: 0.85rem; color: #93c5fd; }

  .verify-list { list-style: none; padding: 0; margin: 0; display: flex;
                 flex-direction: column; gap: 0.4rem; }
  .verify-row { display: grid; grid-template-columns: 1.4rem 1fr auto 1fr;
                gap: 0.5rem; align-items: center; padding: 0.5rem 0.7rem;
                background: #0a0a0c; border: 1px solid #1e3a8a; border-radius: 5px; }
  .verify-row.fail { border-color: #7f1d1d; }
  .verify-mark { font-size: 1rem; color: #34d399; line-height: 1; text-align: center; }
  .verify-row.fail .verify-mark { color: #f87171; }
  .verify-label { font-size: 0.85rem; color: #e4e4e7; }
  .verify-tail { font-size: 0.75rem; color: #a1a1aa; font-family: ui-monospace, monospace;
                 white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .verify-detail { grid-column: 1 / -1; margin-top: 0.3rem; }
  .verify-detail summary { color: #93c5fd; font-size: 0.78rem; cursor: pointer;
                            list-style: none; padding: 0.2rem 0; user-select: none; }
  .verify-detail summary::-webkit-details-marker { display: none; }
  .verify-detail summary::before { content: '▶ '; }
  .verify-detail[open] summary::before { content: '▼ '; }
  .verify-detail pre { margin: 0.3rem 0 0; padding: 0.5rem 0.7rem; background: #0f0f12;
                       border: 1px solid #2a2a30; border-radius: 4px;
                       font-family: ui-monospace, monospace; font-size: 0.75rem;
                       color: #d4d4d8; white-space: pre-wrap; max-height: 12rem;
                       overflow: auto; }

  .leftover-list { display: flex; flex-direction: column; gap: 0.4rem; margin-top: 0.6rem; }
  .leftover-row { background: #0a0a0c; border: 1px solid #2a2a30; border-radius: 5px;
                  padding: 0.4rem 0.7rem; }
  .leftover-row[open] { border-color: #f59e0b; }
  .leftover-row.unknown { background: #0a0c14; border-style: dashed; opacity: 0.7; }
  .leftover-row summary { display: flex; gap: 0.5rem; align-items: center;
                          cursor: pointer; user-select: none; list-style: none;
                          font-size: 0.82rem; }
  .leftover-row summary::-webkit-details-marker { display: none; }
  .leftover-row summary::before { content: '▶'; color: #71717a; font-size: 0.7rem; }
  .leftover-row[open] summary::before { content: '▼'; }
  .leftover-name { background: #0f0f12; padding: 0.1rem 0.4rem; border-radius: 3px;
                   font-family: ui-monospace, monospace; color: #fbbf24; font-size: 0.78rem; }
  .leftover-size { color: #a1a1aa; font-size: 0.75rem; margin-left: auto; }
  .leftover-files { list-style: none; padding: 0.4rem 0 0 1rem; margin: 0;
                    border-top: 1px dashed #2a2a30; margin-top: 0.4rem; }
  .leftover-files li { display: flex; gap: 0.5rem; align-items: center;
                       font-size: 0.75rem; padding: 0.15rem 0; }
  .leftover-files code { background: transparent; padding: 0; color: #cbd5e1;
                         font-family: ui-monospace, monospace; }
  .leftover-files .size { color: #71717a; margin-left: auto; }
  .wipe-log { margin: 0.6rem 0 0; padding: 0.5rem 0.7rem; background: #0a0a0c;
              border: 1px solid #2a2a30; border-radius: 4px;
              font-family: ui-monospace, monospace; font-size: 0.75rem;
              color: #d4d4d8; white-space: pre-wrap; max-height: 12rem;
              overflow: auto; }
</style>
