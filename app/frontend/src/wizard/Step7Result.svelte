<script lang="ts">
  import { _ } from 'svelte-i18n';
  import { wizardStore } from '../stores/wizard';
</script>

<h2>{$_('step.7.title')}</h2>

<p>Cluster: <code>{$wizardStore.inventory.cluster?.name}</code></p>
<p>Run ID: <code>{$wizardStore.runId}</code></p>

<p>The kubeconfig and Ceph dashboard URL will appear here once the backend wires
up the result emitter (TODO).</p>
