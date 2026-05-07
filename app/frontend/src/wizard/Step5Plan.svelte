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
  const topology = $derived(inv.cluster.topology);
  const cpRoles = $derived(inv.nodes.filter((n) => n.roles.includes('control-plane')).length);
  const workerRoles = $derived(inv.nodes.filter((n) => n.roles.includes('worker')).length);
  const cephMons = $derived(inv.nodes.filter((n) => n.roles.includes('ceph-mon')).length);
  const cephOSDs = $derived(inv.nodes.filter((n) => n.roles.includes('ceph-osd')).length);

  const topoLabel = $derived(
    topology === 'ceph-only' ? 'Ceph storage'
    : topology === 'k8s-only' ? 'Kubernetes'
    : 'Ceph + Kubernetes'
  );

  // Pipeline stages that will actually run for this topology.
  const stagesForTopology = $derived(
    topology === 'ceph-only'
      ? ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'ceph']
      : topology === 'k8s-only'
      ? ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'kubernetes', 'addons']
      : ['seed_iso', 'terraform_apply', 'wait_ssh', 'preflight', 'ceph', 'kubernetes', 'csi', 'addons']
  );

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

<StepNav canAdvance={consented} />

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
  .consent { display: flex; gap: 0.5rem; align-items: flex-start; font-size: 0.85rem;
             color: #d4d4d8; cursor: pointer; line-height: 1.5; }
  .consent input { margin-top: 0.2rem; accent-color: #3b82f6; }
</style>
