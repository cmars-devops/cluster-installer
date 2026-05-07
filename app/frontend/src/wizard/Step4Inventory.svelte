<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { gotoStep, wizardStore } from '../stores/wizard';
  import { api } from '../lib/api';

  let yamlText = $state('# YAML preview will be generated from form fields');
  let result = $state<{ valid: boolean; errors: string[] } | null>(null);

  async function validate() {
    if (!$wizardStore.contentDir) return;
    result = await api.validateInventory(yamlText, $wizardStore.contentDir);
  }
</script>

<h2>{$_('step.4.title')}</h2>

<textarea bind:value={yamlText} rows="20"></textarea>

<div class="row">
  <button onclick={() => gotoStep(2)}>{$_('common.back')}</button>
  <button onclick={validate}>Validate</button>
  <button disabled={!result?.valid} onclick={() => gotoStep(4)}>{$_('common.next')}</button>
</div>

{#if result}
  <pre class:ok={result.valid} class:err={!result.valid}>
{result.valid ? 'Valid' : result.errors.join('\n')}
  </pre>
{/if}

<style>
  textarea { width: 100%; font-family: ui-monospace, monospace; padding: 0.5rem;
             background: #1f1f23; color: inherit; border: 1px solid #3f3f46; border-radius: 4px; }
  .row { display: flex; gap: 0.5rem; margin-top: 1rem; }
  pre.ok  { color: #4ade80; }
  pre.err { color: #f87171; white-space: pre-wrap; }
</style>
