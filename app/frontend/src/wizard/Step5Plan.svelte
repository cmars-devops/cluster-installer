<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore, gotoStep } from '../stores/wizard';
  import { api } from '../lib/api';

  // ── State machine — explicit so the UI can answer "what's happening?"
  // idle    — never planned, user must click "Preview"
  // loading — terraform init+plan running on the Go backend (~30s–2min)
  // ready   — plan output available, user can review and consent
  // error   — last attempt failed; show the message, allow retry
  type Phase = 'idle' | 'loading' | 'ready' | 'error';

  let phase = $state<Phase>('idle');
  let preview = $state('');
  let errMsg = $state('');
  let consented = $state(false);

  // Live elapsed counter while loading — answers "did it freeze?"
  let elapsed = $state(0);
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;

  const inv = $derived($wizardStore.inventory);
  const topology = $derived(inv.cluster.topology);
  const devVMMode = $derived(topology === 'dev-vm');
  const devVMNode = $derived(devVMMode ? inv.nodes[0] : undefined);
  const cpRoles = $derived(inv.nodes.filter((n) => n.roles.includes('control-plane')).length);
  const workerRoles = $derived(inv.nodes.filter((n) => n.roles.includes('worker')).length);
  const cephMons = $derived(inv.nodes.filter((n) => n.roles.includes('ceph-mon')).length);
  const cephOSDs = $derived(inv.nodes.filter((n) => n.roles.includes('ceph-osd')).length);

  const topoLabel = $derived(
    topology === 'dev-vm'    ? 'Dev VM (single)'
    : topology === 'ceph-only' ? 'Ceph storage'
    : topology === 'k8s-only'  ? 'Kubernetes'
    : 'Ceph + Kubernetes'
  );

  // Pipeline stages that will actually run for this topology.
  const stagesForTopology = $derived(
    topology === 'dev-vm'
      ? ['seed_iso', 'datastore_upload', 'terraform_apply', 'wait_ssh', 'verify']
      : topology === 'ceph-only'
      ? ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'ceph']
      : topology === 'k8s-only'
      ? ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'kubernetes', 'addons']
      : ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'ceph', 'kubernetes', 'csi', 'addons']
  );

  // Progressive checklist — what gates entry to Step 6 (Apply).
  const ready = $derived(phase === 'ready');
  const canAdvance = $derived(ready && consented);

  function startElapsed() {
    elapsed = 0;
    if (elapsedTimer) clearInterval(elapsedTimer);
    elapsedTimer = setInterval(() => { elapsed += 1; }, 1000);
  }
  function stopElapsed() {
    if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
  }

  async function loadPlan() {
    phase = 'loading';
    errMsg = '';
    preview = '';
    startElapsed();
    try {
      if (!$wizardStore.runId) {
        const id = await api.createRun(inv);
        wizardStore.update((s) => ({ ...s, runId: id }));
      }
      const out = await api.planRun($wizardStore.runId!);
      preview = out;
      phase = 'ready';
    } catch (e) {
      errMsg = String(e);
      phase = 'error';
    } finally {
      stopElapsed();
    }
  }

  async function startApply() {
    gotoStep(5);
  }

  // Hint text per phase — surfaces "wait or proceed?" prominently.
  const phaseHint = $derived(
    phase === 'idle'    ? '아직 plan을 실행하지 않았습니다. "미리보기 실행"을 누르세요.'
    : phase === 'loading' ? `실행 중입니다. terraform이 인프라 변경사항을 계산하는 데 보통 30초~2분이 걸립니다. 이 화면에서 기다리세요. (${elapsed}s 경과)`
    : phase === 'error'   ? '실패했습니다. 아래 오류를 확인하고 "다시 시도"를 누르세요.'
    : 'plan 결과가 준비됐습니다. 결과를 검토하고 동의 체크 후 다음 단계로 진행하세요.'
  );
  const phaseTone = $derived(
    phase === 'loading' ? 'info'
    : phase === 'error'   ? 'danger'
    : phase === 'ready'   ? 'success'
    : 'muted'
  );
</script>

<header class="step-header">
  <h2>{$_('step.5.title')}</h2>
  <p>{$_('step.5.subtitle')}</p>
</header>

{#if devVMMode && devVMNode}
  <Section title="이 VM이 만들어집니다" subtitle="단일 독립 VM — 무인 자동 설치 + 자동 검증">
    <div class="summary-grid">
      <div class="card">
        <h4>{devVMNode.hostname || '(unnamed)'}</h4>
        <p>{devVMNode.os}{devVMNode.os_version ? ' ' + devVMNode.os_version : ''}</p>
        <p class="muted">{(devVMNode.ip_mode ?? 'static') === 'dhcp' ? 'DHCP' : (devVMNode.ip || '(IP unset)')}</p>
      </div>
      <div class="card">
        <h4>자원</h4>
        <p>{devVMNode.cpu}c · {devVMNode.memory_gb}GB · {devVMNode.disk_gb}GB</p>
        <p class="muted">{devVMNode.disk_provisioning ?? 'thin'}</p>
      </div>
      <div class="card">
        <h4>데이터스토어</h4>
        <p class="trunc">{devVMNode.datastore || inv.target.datastore || '(unset)'}</p>
        <p class="muted">{inv.target.network || 'VM Network'}</p>
      </div>
      <div class="card">
        <h4>ESXi</h4>
        <p class="trunc">{inv.target.endpoint || '(not set)'}</p>
        <p class="muted">{inv.target.username || 'root'}</p>
      </div>
    </div>

    <div class="pipeline">
      <div class="pipeline-label">실행될 단계 ({stagesForTopology.length}):</div>
      <div class="pipeline-flow">
        {#each stagesForTopology as st, i}
          {#if i > 0}<span class="arrow">→</span>{/if}
          <span class="stage-pill">{st}</span>
        {/each}
      </div>
    </div>
  </Section>
{:else}
  <Section title={$_('step5.summary')} subtitle={topoLabel}>
    <div class="summary-grid">
      <div class="card">
        <h4>{inv.cluster.name}</h4>
        <p>{inv.cluster.domain}</p>
        <p class="muted">{topoLabel}</p>
      </div>
      {#if topology !== 'ceph-only'}
        <div class="card">
          <h4>{inv.cluster.kubernetes.distro.toUpperCase()}</h4>
          <p>{inv.cluster.kubernetes.version}</p>
          <p class="muted">{inv.cluster.kubernetes.cni}</p>
        </div>
      {/if}
      {#if topology !== 'k8s-only'}
        <div class="card">
          <h4>Ceph</h4>
          <p>mon × {cephMons}, osd × {cephOSDs}</p>
        </div>
      {/if}
      <div class="card">
        <h4>{inv.nodes.length} nodes</h4>
        <p>{topology === 'ceph-only'
              ? `mon: ${cephMons}, osd: ${cephOSDs}`
              : topology === 'k8s-only'
              ? `cp: ${cpRoles}, worker: ${workerRoles}`
              : `cp: ${cpRoles}, worker: ${workerRoles}, mon: ${cephMons}, osd: ${cephOSDs}`}</p>
      </div>
      <div class="card">
        <h4>{inv.target.type}</h4>
        <p class="trunc">{inv.target.endpoint || '(not set)'}</p>
      </div>
    </div>

    <div class="pipeline">
      <div class="pipeline-label">실행될 단계 ({stagesForTopology.length}):</div>
      <div class="pipeline-flow">
        {#each stagesForTopology as st, i}
          {#if i > 0}<span class="arrow">→</span>{/if}
          <span class="stage-pill">{st}</span>
        {/each}
      </div>
    </div>
  </Section>
{/if}

<Section title="terraform plan">
  <!-- Phase status banner — always visible, answers "what now?" at a glance -->
  <div class="status-banner status-{phaseTone}">
    <div class="status-head">
      <span class="status-dot" class:spinning={phase === 'loading'}></span>
      <strong>
        {#if phase === 'idle'}대기 중
        {:else if phase === 'loading'}실행 중… ({elapsed}s)
        {:else if phase === 'ready'}완료
        {:else}오류{/if}
      </strong>
    </div>
    <p class="status-msg">{phaseHint}</p>
  </div>

  <div class="row">
    <Button variant="secondary" disabled={phase === 'loading'} onclick={loadPlan}>
      {#if phase === 'loading'}
        실행 중… ({elapsed}s)
      {:else if phase === 'error' || phase === 'ready'}
        다시 실행
      {:else}
        {$_('step5.previewBtn')}
      {/if}
    </Button>
    {#if phase === 'ready'}
      <Badge tone="info">plan ready</Badge>
    {/if}
  </div>

  {#if phase === 'error'}
    <pre class="plan plan-error">{errMsg}</pre>
  {:else}
    <pre class="plan">{preview || $_('step5.noPlan')}</pre>
  {/if}
</Section>

<Section title="다음 단계 진입 조건">
  <ol class="checklist">
    <li class:done={ready}>
      <span class="check">{ready ? '✓' : '○'}</span>
      <span>terraform plan 완료 (이 단계)</span>
    </li>
    <li class:done={ready && consented}>
      <span class="check">{ready && consented ? '✓' : '○'}</span>
      <label class="consent-inline">
        <input type="checkbox" bind:checked={consented} disabled={!ready} />
        <span>{$_('step5.consent')}</span>
      </label>
    </li>
  </ol>
  <div class="row">
    <Button variant="primary" disabled={!canAdvance} onclick={startApply}>
      {$_('step5.applyBtn')} →
    </Button>
    {#if !ready}<span class="muted-hint">먼저 plan을 완료하세요.</span>
    {:else if !consented}<span class="muted-hint">동의에 체크하세요.</span>{/if}
  </div>
</Section>

<StepNav canAdvance={canAdvance} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .summary-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
                  gap: 0.75rem; }
  .card { background: #0f0f12; border: 1px solid #2a2a30; border-radius: 6px; padding: 0.85rem; }
  .card h4 { margin: 0 0 0.3rem; font-size: 0.95rem; }
  .card p { margin: 0; font-size: 0.8rem; color: #d4d4d8; }
  .card .muted { color: #71717a; }
  .trunc { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pipeline { margin-top: 1rem; padding-top: 0.75rem; border-top: 1px solid #2a2a30; }
  .pipeline-label { font-size: 0.78rem; color: #a1a1aa; margin-bottom: 0.4rem; }
  .pipeline-flow { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
  .stage-pill { display: inline-block; padding: 0.2rem 0.55rem; border-radius: 999px;
                background: #1e293b; border: 1px solid #3b82f6; color: #93c5fd;
                font-size: 0.7rem; font-family: ui-monospace, monospace; }
  .arrow { color: #52525b; font-size: 0.8rem; }
  .row { display: flex; gap: 0.75rem; align-items: center; margin-top: 0.5rem; }
  .plan { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
          border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.8rem;
          max-height: 50vh; overflow: auto; color: #d4d4d8; margin: 0.5rem 0 0; white-space: pre-wrap; }
  .plan-error { border-color: #b91c1c; color: #fca5a5; background: #1a0a0a; }

  /* Phase status banner */
  .status-banner { padding: 0.7rem 0.85rem; border-radius: 6px; border: 1px solid;
                   margin-bottom: 0.6rem; }
  .status-info    { background: #0c1a2e; border-color: #1e40af; color: #bfdbfe; }
  .status-success { background: #0a1d12; border-color: #166534; color: #86efac; }
  .status-danger  { background: #1f0a0a; border-color: #b91c1c; color: #fecaca; }
  .status-muted   { background: #16161a; border-color: #3f3f46; color: #a1a1aa; }
  .status-head { display: flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; }
  .status-msg  { margin: 0.2rem 0 0; font-size: 0.78rem; line-height: 1.5; }
  .status-dot  { width: 9px; height: 9px; border-radius: 50%;
                 background: currentColor; opacity: 0.85; flex-shrink: 0; }
  .status-dot.spinning { animation: pulse 1s ease-in-out infinite; }
  @keyframes pulse {
    0%, 100% { opacity: 0.4; transform: scale(0.85); }
    50%      { opacity: 1.0; transform: scale(1.15); }
  }

  /* Progressive checklist */
  .checklist { list-style: none; padding: 0; margin: 0 0 0.75rem; }
  .checklist li { display: flex; align-items: flex-start; gap: 0.55rem;
                  padding: 0.4rem 0; font-size: 0.85rem; color: #71717a; }
  .checklist li.done { color: #d4d4d8; }
  .checklist .check { display: inline-block; width: 1.1rem; text-align: center;
                      font-weight: 700; color: #52525b; }
  .checklist li.done .check { color: #4ade80; }
  .consent-inline { display: flex; gap: 0.4rem; align-items: flex-start;
                    cursor: pointer; line-height: 1.5; }
  .consent-inline input { margin-top: 0.2rem; accent-color: #3b82f6; }
  .muted-hint { font-size: 0.75rem; color: #71717a; }
</style>
