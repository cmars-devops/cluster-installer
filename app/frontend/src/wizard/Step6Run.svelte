<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';
  import { onMount } from 'svelte';

  const stages = [
    'pending', 'seed_iso',
    'terraform_init', 'terraform_plan', 'terraform_apply',
    'wait_ssh', 'preflight', 'ceph', 'kubernetes', 'csi', 'addons',
    'completed'
  ] as const;

  let lines = $state<string[]>([]);
  let stage = $state<string>('pending');
  let serverURL = $state('');
  let firewallHint = $state('');
  let starting = $state(false);
  let done = $state(false);

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
    return () => { off1?.(); off2?.(); off3?.(); off4?.(); };
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

  function stageTone(s: string, current: string): 'neutral' | 'success' | 'info' | 'danger' {
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
      <div class="stage" class:active={s === stage}>
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
  {/if}
</Section>

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
  .row { display: flex; gap: 0.75rem; margin-top: 0.75rem; }
  .log { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
         border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.78rem;
         max-height: 50vh; overflow: auto; color: #d4d4d8; margin: 0; line-height: 1.5;
         white-space: pre-wrap; }
</style>
