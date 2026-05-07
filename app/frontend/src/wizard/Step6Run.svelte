<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { gotoStep, wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';
  import { onMount } from 'svelte';

  let lines = $state<string[]>([]);
  let stage = $state('pending');
  let serverURL = $state('');
  let firewallHint = $state('');
  let done = $state(false);
  let starting = $state(false);

  onMount(() => {
    // @ts-expect-error  injected by Wails runtime
    const off1 = window.runtime?.EventsOn?.('run:line', (l: string) => {
      lines = [...lines, l];
    });
    // @ts-expect-error
    const off2 = window.runtime?.EventsOn?.('run:stage', (s: string, msg?: string) => {
      stage = s;
      if (msg) lines = [...lines, `[${s}] ${msg}`];
    });
    // @ts-expect-error
    const off3 = window.runtime?.EventsOn?.('run:server-listening', (d: { url: string }) => {
      serverURL = d.url;
    });
    // @ts-expect-error
    const off4 = window.runtime?.EventsOn?.('run:firewall-hint', (d: { url: string; note: string }) => {
      firewallHint = d.note;
    });
    return () => { off1?.(); off2?.(); off3?.(); off4?.(); };
  });

  async function start() {
    if (!$wizardStore.runId || starting) return;
    starting = true;
    try {
      await api.applyRun($wizardStore.runId);
      done = true;
    } catch (e) {
      lines = [...lines, 'ERROR: ' + e];
    }
  }
</script>

<h2>{$_('step.6.title')}</h2>

<div class="status">
  <div>Stage: <strong>{stage}</strong></div>
  {#if serverURL}
    <div class="server">
      Install server: <code>{serverURL}</code>
      <span class="hint">VMs fetch Agama profiles from this URL.</span>
    </div>
  {/if}
  {#if firewallHint}
    <div class="warn">⚠ {firewallHint}</div>
  {/if}
</div>

<button onclick={start} disabled={!$wizardStore.runId || starting}>
  {starting ? $_('common.loading') : 'Start'}
</button>

<pre>{lines.join('\n')}</pre>

<button disabled={!done} onclick={() => gotoStep(6)}>{$_('common.next')}</button>

<style>
  .status { margin: 1rem 0; padding: 0.75rem; border: 1px solid #3f3f46;
            border-radius: 6px; background: #18181b; }
  .server { margin-top: 0.5rem; font-size: 0.9rem; }
  .server .hint { color: #a1a1aa; margin-left: 0.5rem; }
  .warn { margin-top: 0.5rem; color: #fbbf24; font-size: 0.9rem; }
  pre { background: #1f1f23; padding: 0.75rem; border-radius: 4px;
        max-height: 50vh; overflow: auto; font-family: ui-monospace, monospace; }
  code { background: #27272a; padding: 0.1rem 0.4rem; border-radius: 3px; }
</style>
