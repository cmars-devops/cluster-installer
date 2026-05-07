<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { wizardStore, gotoStep } from '../stores/wizard';
  import { api } from '../lib/api';

  let ref = $state('v0.1.0');
  let busy = $state(false);
  let msg = $state('');

  async function fetchAndNext() {
    busy = true; msg = '';
    try {
      const dir = await api.fetchContent('', ref);
      wizardStore.update((s) => ({ ...s, contentRef: ref, contentDir: dir }));
      gotoStep(1);
    } catch (e) {
      msg = String(e);
    } finally {
      busy = false;
    }
  }
</script>

<h2>{$_('step.1.title')}</h2>
<p>{$_('common.loading')}</p>

<label>
  Content tag
  <input bind:value={ref} placeholder="v0.1.0" />
</label>

<button disabled={busy} onclick={fetchAndNext}>{$_('common.next')}</button>
{#if msg}<p class="err">{msg}</p>{/if}

<style>
  label { display: block; margin: 1rem 0; }
  input { padding: 0.4rem; background: #1f1f23; color: inherit; border: 1px solid #3f3f46; border-radius: 4px; }
  .err { color: #f87171; }
</style>
