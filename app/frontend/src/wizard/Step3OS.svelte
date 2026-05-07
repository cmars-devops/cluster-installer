<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { gotoStep } from '../stores/wizard';
</script>

<h2>{$_('step.3.title')}</h2>
<p>OS image catalog will load from <code>content/images.yaml</code> (TODO).</p>

<div class="row">
  <button onclick={() => gotoStep(1)}>{$_('common.back')}</button>
  <button onclick={() => gotoStep(3)}>{$_('common.next')}</button>
</div>

<style>
  .row { display: flex; gap: 0.5rem; margin-top: 1rem; }
</style>
