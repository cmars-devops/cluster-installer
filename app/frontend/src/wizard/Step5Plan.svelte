<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore, gotoStep } from '../stores/wizard';
  import { api } from '../lib/api';

  let preview = $state('');
  let busy = $state(false);
  let consented = $state(false);

  const inv = $derived($wizardStore.inventory);
  const cpRoles = $derived(inv.nodes.filter((n) => n.roles.includes('control-plane')).length);
  const workerRoles = $derived(inv.nodes.filter((n) => n.roles.includes('worker')).length);
  const cephOSDs = $derived(inv.nodes.filter((n) => n.roles.includes('ceph-osd')).length);

  async function loadPlan() {
    if (!$wizardStore.runId) {
      // Auto-create a run from the current inventory if needed.
      const id = await api.createRun(inv);
      wizardStore.update((s) => ({ ...s, runId: id }));
    }
    busy = true;
    preview = await api.planRun($wizardStore.runId!);
    busy = false;
  }

  async function startApply() {
    // Step 6 wires the actual ApplyRun call + listens to events.
    gotoStep(5);
  }
</script>

<header class="step-header">
  <h2>{$_('step.5.title')}</h2>
  <p>{$_('step.5.subtitle')}</p>
</header>

<Section title={$_('step5.summary')}>
  <div class="summary-grid">
    <div class="card">
      <h4>{inv.cluster.name}</h4>
      <p>{inv.cluster.domain}</p>
    </div>
    <div class="card">
      <h4>{inv.cluster.kubernetes.distro.toUpperCase()}</h4>
      <p>{inv.cluster.kubernetes.version}</p>
      <p class="muted">{inv.cluster.kubernetes.cni}</p>
    </div>
    <div class="card">
      <h4>{inv.nodes.length} nodes</h4>
      <p>cp: {cpRoles}, worker: {workerRoles}, ceph-osd: {cephOSDs}</p>
    </div>
    <div class="card">
      <h4>{inv.target.type}</h4>
      <p class="trunc">{inv.target.endpoint || '(not set)'}</p>
    </div>
  </div>
</Section>

<Section title="terraform plan">
  <div class="row">
    <Button variant="secondary" disabled={busy} onclick={loadPlan}>
      {busy ? $_('common.loading') : $_('step5.previewBtn')}
    </Button>
    {#if preview}
      <Badge tone="info">plan ready</Badge>
    {/if}
  </div>

  <pre class="plan">{preview || $_('step5.noPlan')}</pre>
</Section>

<Section title="">
  <label class="consent">
    <input type="checkbox" bind:checked={consented} />
    <span>{$_('step5.consent')}</span>
  </label>
  <div class="row">
    <Button variant="primary" disabled={!consented || !preview} onclick={startApply}>
      {$_('step5.applyBtn')} →
    </Button>
  </div>
</Section>

<StepNav canAdvance={consented && !!preview} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .summary-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.75rem; }
  .card { background: #0f0f12; border: 1px solid #2a2a30; border-radius: 6px; padding: 0.85rem; }
  .card h4 { margin: 0 0 0.3rem; font-size: 0.95rem; }
  .card p { margin: 0; font-size: 0.8rem; color: #d4d4d8; }
  .card .muted { color: #71717a; }
  .trunc { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .row { display: flex; gap: 0.75rem; align-items: center; margin-top: 0.5rem; }
  .plan { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
          border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.8rem;
          max-height: 50vh; overflow: auto; color: #d4d4d8; margin: 0.5rem 0 0; white-space: pre-wrap; }
  .consent { display: flex; gap: 0.5rem; align-items: flex-start; font-size: 0.85rem;
             color: #d4d4d8; cursor: pointer; line-height: 1.5; }
  .consent input { margin-top: 0.2rem; accent-color: #3b82f6; }
</style>
