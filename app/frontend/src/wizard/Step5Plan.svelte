<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { gotoStep, wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';

  let preview = $state('');
  let busy = $state(false);

  async function loadPlan() {
    if (!$wizardStore.runId) return;
    busy = true;
    preview = await api.planRun($wizardStore.runId);
    busy = false;
  }
</script>

<h2>{$_('step.5.title')}</h2>

<button onclick={loadPlan} disabled={busy}>terraform plan</button>
<pre>{preview || '(no plan yet)'}</pre>

<div class="row">
  <button onclick={() => gotoStep(3)}>{$_('common.back')}</button>
  <button onclick={() => gotoStep(5)}>{$_('common.apply')}</button>
</div>

<style>
  pre { background: #1f1f23; padding: 0.75rem; border-radius: 4px; max-height: 50vh; overflow: auto; }
  .row { display: flex; gap: 0.5rem; margin-top: 1rem; }
</style>
