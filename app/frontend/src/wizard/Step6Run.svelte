<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { gotoStep, wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';
  import { onMount } from 'svelte';

  let lines = $state<string[]>([]);
  let stage = $state('pending');
  let done = $state(false);

  onMount(() => {
    // Subscribe to Wails events emitted by the Go backend.
    // @ts-expect-error  runtime injected by Wails
    const off1 = window.runtime?.EventsOn?.('run:line',  (l: string) => lines = [...lines, l]);
    // @ts-expect-error
    const off2 = window.runtime?.EventsOn?.('run:stage', (s: string) => stage = s);
    return () => { off1?.(); off2?.(); };
  });

  async function start() {
    if (!$wizardStore.runId) return;
    try {
      await api.applyRun($wizardStore.runId);
      done = true;
    } catch (e) {
      lines = [...lines, 'ERROR: ' + e];
    }
  }
</script>

<h2>{$_('step.6.title')}</h2>
<p>Stage: <strong>{stage}</strong></p>

<button onclick={start} disabled={!$wizardStore.runId}>Start</button>

<pre>{lines.join('\n')}</pre>

<button disabled={!done} onclick={() => gotoStep(6)}>{$_('common.next')}</button>

<style>
  pre { background: #1f1f23; padding: 0.75rem; border-radius: 4px;
        max-height: 60vh; overflow: auto; font-family: ui-monospace, monospace; }
</style>
