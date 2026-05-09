<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';
  import { api, type VerifyCheck } from '../lib/api';
  import { onMount } from 'svelte';

  // Stage list includes verify between wait_ssh and preflight. For
  // cluster topologies the orchestrator emits stage-skipped on verify
  // (it's dev-vm only) so the badge dims correctly.
  const stages = [
    'pending', 'seed_iso', 'datastore_upload',
    'terraform_init', 'terraform_plan', 'terraform_apply',
    'wait_ssh', 'verify', 'preflight', 'ceph', 'kubernetes', 'csi', 'addons',
    'completed'
  ] as const;

  let lines = $state<string[]>([]);
  let stage = $state<string>('pending');
  let skipped = $state<Record<string, string>>({});   // stage → reason
  let serverURL = $state('');
  let firewallHint = $state('');
  let starting = $state(false);
  let done = $state(false);
  let verifyChecks = $state<VerifyCheck[]>([]);

  const topology = $derived($wizardStore.inventory.cluster.topology);
  const devVMMode = $derived(topology === 'dev-vm');

  onMount(() => {
    // Wails runtime injects window.runtime.EventsOn when running under the desktop app.
    // In a plain browser this is a no-op.
    const r: any = (window as any).runtime;
    const off1 = r?.EventsOn?.('run:line', (l: string) => { lines = [...lines, l]; });
    const off2 = r?.EventsOn?.('run:stage', (s: string, msg?: string) => {
      stage = s;
      if (msg) lines = [...lines, `[${s}] ${msg}`];
      if (s === 'completed') done = true;
    });
    const off3 = r?.EventsOn?.('run:server-listening', (d: { url: string }) => { serverURL = d.url; });
    const off4 = r?.EventsOn?.('run:firewall-hint', (d: { note: string }) => { firewallHint = d.note; });
    // Topology gating: orchestrator emits run:stage-skipped before the loop
    // starts so we can mark stages as intentionally skipped (vs failed).
    const off5 = r?.EventsOn?.('run:stage-skipped', (s: string, reason: string) => {
      skipped = { ...skipped, [s]: reason };
      lines = [...lines, `[skip] ${s} — ${reason}`];
    });
    // Per-check verify result. Replaces the matching id row if it
    // already exists so re-runs of redeploy show fresh data.
    const off6 = r?.EventsOn?.('run:verify', (rec: VerifyCheck) => {
      const idx = verifyChecks.findIndex((c) => c.id === rec.id);
      if (idx >= 0) verifyChecks = verifyChecks.map((c, i) => i === idx ? rec : c);
      else verifyChecks = [...verifyChecks, rec];
    });
    return () => { off1?.(); off2?.(); off3?.(); off4?.(); off5?.(); off6?.(); };
  });

  async function start() {
    if (!$wizardStore.runId || starting) return;
    starting = true;
    lines = [`▶ Starting run ${$wizardStore.runId}...`];
    try {
      await api.applyRun($wizardStore.runId);
    } catch (e) {
      lines = [...lines, 'ERROR: ' + e];
    }
  }

  let cancelling = $state(false);
  async function cancel() {
    if (!$wizardStore.runId || cancelling) return;
    if (!confirm($_('step6.cancelConfirm'))) return;
    cancelling = true;
    lines = [...lines, '⨯ Cancelling run — waiting for current stage to abort...'];
    try {
      await api.cancelRun($wizardStore.runId);
    } catch (e) {
      lines = [...lines, 'ERROR: ' + e];
    } finally {
      cancelling = false;
    }
  }

  // Cancel is enabled while a run is mid-flight — i.e. starting flag set,
  // and the stage has not yet reached a terminal state.
  let canCancel = $derived(starting && stage !== 'completed' && stage !== 'failed');

  function stageTone(s: string, current: string): 'neutral' | 'success' | 'info' | 'danger' {
    if (skipped[s]) return 'neutral';            // topology said this stage doesn't run
    const idx = stages.indexOf(s as any);
    const cur = stages.indexOf(current as any);
    if (s === 'failed') return 'danger';
    if (idx === -1 || cur === -1) return 'neutral';
    if (idx < cur) return 'success';
    if (idx === cur) return 'info';
    return 'neutral';
  }
</script>

<header class="step-header">
  <h2>{$_('step.6.title')}</h2>
  <p>{$_('step.6.subtitle')}</p>
</header>

{#if serverURL}
  <Section title={$_('step6.serverURL')} subtitle={$_('step6.serverURLHint')}>
    <code class="url">{serverURL}</code>
    {#if firewallHint}
      <p class="fw-hint">⚠ {firewallHint}</p>
    {/if}
  </Section>
{/if}

<Section title={$_('step6.stage')}>
  <div class="stage-flow">
    {#each stages as s}
      <div class="stage" class:active={s === stage} class:skipped={!!skipped[s]} title={skipped[s] ?? ''}>
        <Badge tone={stageTone(s, stage)}>
          {$_('step6.stages.' + s)}
        </Badge>
      </div>
    {/each}
  </div>

  {#if !starting}
    <div class="row">
      <Button variant="primary" disabled={!$wizardStore.runId} onclick={start}>
        {$_('common.start')} →
      </Button>
    </div>
  {:else if canCancel}
    <div class="row">
      <Button variant="danger" disabled={cancelling} onclick={cancel}>
        ⨯ {$_('step6.cancel')}
      </Button>
    </div>
  {/if}
</Section>

{#if devVMMode && verifyChecks.length > 0}
  <Section title={$_('step6.verifyTitle')} subtitle={$_('step6.verifySubtitle')}>
    <ul class="verify-list">
      {#each verifyChecks as v}
        <li class="verify-row" class:fail={!v.pass}>
          <span class="verify-mark">{v.pass ? '✓' : '✗'}</span>
          <span class="verify-label">{v.label}</span>
          <Badge tone={v.pass ? 'success' : 'danger'}>{v.pass ? 'PASS' : 'FAIL'}</Badge>
          {#if v.detail}
            <details class="verify-detail">
              <summary>{$_('step6.verifyDetail')}</summary>
              <pre>{v.detail}</pre>
            </details>
          {/if}
        </li>
      {/each}
    </ul>
  </Section>
{/if}

<Section title={$_('step6.log')}>
  <pre class="log">{lines.length === 0 ? '(아직 로그 없음 / no logs yet)' : lines.join('\n')}</pre>
</Section>

<StepNav canAdvance={done} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .url { display: inline-block; background: #0f0f12; padding: 0.4rem 0.7rem;
         border-radius: 4px; font-family: ui-monospace, monospace; font-size: 0.85rem; color: #93c5fd; }
  .fw-hint { margin-top: 0.5rem; font-size: 0.8rem; color: #fbbf24; }
  .stage-flow { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; }
  .stage { transition: transform 0.1s; }
  .stage.active { transform: scale(1.05); }
  .stage.skipped { opacity: 0.4; text-decoration: line-through; }
  .row { display: flex; gap: 0.75rem; margin-top: 0.75rem; }
  .log { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
         border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.78rem;
         max-height: 50vh; overflow: auto; color: #d4d4d8; margin: 0; line-height: 1.5;
         white-space: pre-wrap; }
  .verify-list { list-style: none; padding: 0; margin: 0; display: flex;
                 flex-direction: column; gap: 0.4rem; }
  .verify-row { display: grid; grid-template-columns: 1.4rem 1fr auto auto;
                gap: 0.5rem; align-items: center; padding: 0.5rem 0.7rem;
                background: #0a0a0c; border: 1px solid #1e3a8a; border-radius: 5px; }
  .verify-row.fail { border-color: #7f1d1d; }
  .verify-mark { font-size: 1rem; color: #34d399; line-height: 1; text-align: center; }
  .verify-row.fail .verify-mark { color: #f87171; }
  .verify-label { font-size: 0.85rem; color: #e4e4e7; }
  .verify-detail { grid-column: 1 / -1; margin-top: 0.3rem; }
  .verify-detail summary { color: #93c5fd; font-size: 0.78rem; cursor: pointer;
                            list-style: none; padding: 0.2rem 0; }
  .verify-detail summary::-webkit-details-marker { display: none; }
  .verify-detail summary::before { content: '▶ '; }
  .verify-detail[open] summary::before { content: '▼ '; }
  .verify-detail pre { margin: 0.3rem 0 0; padding: 0.5rem 0.7rem; background: #0f0f12;
                       border: 1px solid #2a2a30; border-radius: 4px;
                       font-family: ui-monospace, monospace; font-size: 0.75rem;
                       color: #d4d4d8; white-space: pre-wrap; max-height: 12rem;
                       overflow: auto; }
</style>
