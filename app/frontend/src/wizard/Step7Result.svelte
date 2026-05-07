<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Button from '../lib/ui/Button.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';

  const inv = $derived($wizardStore.inventory);

  let copied = $state<string | null>(null);
  function copy(value: string, label: string) {
    navigator.clipboard?.writeText(value);
    copied = label;
    setTimeout(() => (copied = null), 1500);
  }

  // In production these come from Wails events emitted at the end of ApplyRun
  // (run:result with kubeconfig path, dashboard URL, generated passwords).
  // For now, derive what we can from inventory; placeholders for the rest.
  const kubeAPI = $derived(`https://${inv.network.vip}:6443`);
  const cephDashboard = $derived(
    inv.nodes.find((n) => n.roles.includes('ceph-mon'))
      ? `https://${inv.nodes.find((n) => n.roles.includes('ceph-mon'))!.ip}:8443`
      : '(no ceph-mon node)'
  );
  const ingressURL = $derived(`https://${inv.network.ingress_lb_ip}/`);
</script>

<header class="step-header">
  <h2>{$_('step.7.title')}</h2>
  <p>{$_('step.7.subtitle')}</p>
</header>

<Section title={$_('step7.kubeconfig')} subtitle={$_('step7.kubeconfigDesc')}>
  <p class="muted">
    %LOCALAPPDATA%\cluster-installer\runs\{$wizardStore.runId ?? '<run-id>'}\kubeconfig
  </p>
  <div class="row">
    <Button variant="primary">{$_('step7.downloadKubeconfig')}</Button>
    <Button variant="secondary" onclick={() => copy('export KUBECONFIG=%LOCALAPPDATA%\\cluster-installer\\runs\\' + $wizardStore.runId + '\\kubeconfig', 'env')}>
      {$_('common.copy')} env
    </Button>
    {#if copied === 'env'}<Badge tone="success">copied</Badge>{/if}
  </div>
</Section>

<Section title={$_('step7.endpoints')}>
  <div class="endpoints">
    <div class="endpoint">
      <span class="label">{$_('step7.kubeAPI')}</span>
      <code>{kubeAPI}</code>
      <Button variant="ghost" onclick={() => copy(kubeAPI, 'kubeAPI')}>{$_('common.copy')}</Button>
      {#if copied === 'kubeAPI'}<Badge tone="success">copied</Badge>{/if}
    </div>

    {#if cephDashboard.startsWith('https://')}
      <div class="endpoint">
        <span class="label">{$_('step7.cephDashboard')}</span>
        <code>{cephDashboard}</code>
        <Button variant="ghost" onclick={() => copy(cephDashboard, 'ceph')}>{$_('common.copy')}</Button>
        {#if copied === 'ceph'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}

    {#if inv.addons.ingress !== 'none'}
      <div class="endpoint">
        <span class="label">{$_('step7.ingress')}</span>
        <code>{ingressURL}</code>
        <Button variant="ghost" onclick={() => copy(ingressURL, 'ingress')}>{$_('common.copy')}</Button>
        {#if copied === 'ingress'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}

    {#if inv.addons.gitops === 'argocd'}
      <div class="endpoint">
        <span class="label">{$_('step7.argocd')}</span>
        <code>{ingressURL}argocd</code>
        <Button variant="ghost" onclick={() => copy(ingressURL + 'argocd', 'argo')}>{$_('common.copy')}</Button>
        {#if copied === 'argo'}<Badge tone="success">copied</Badge>{/if}
      </div>
    {/if}
  </div>
</Section>

<Section title={$_('step7.nextSteps')}>
  <ol class="next-steps">
    <li><code>kubectl get nodes</code></li>
    <li><code>ssh root@{inv.nodes.find((n) => n.roles.includes('ceph-mon'))?.ip ?? '<mon>'} ceph -s</code></li>
    {#if inv.addons.gitops === 'argocd'}
      <li>Argo CD에 첫 Application 등록</li>
    {/if}
  </ol>
</Section>

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .row { display: flex; gap: 0.5rem; align-items: center; }
  .muted { color: #71717a; font-size: 0.8rem; font-family: ui-monospace, monospace;
           background: #0f0f12; padding: 0.4rem 0.6rem; border-radius: 4px; margin: 0 0 0.5rem; }
  .endpoints { display: flex; flex-direction: column; gap: 0.5rem; }
  .endpoint { display: grid; grid-template-columns: 12rem 1fr auto auto; gap: 0.5rem; align-items: center; }
  .endpoint .label { color: #a1a1aa; font-size: 0.85rem; }
  .endpoint code { background: #0f0f12; padding: 0.3rem 0.5rem; border-radius: 4px;
                   font-family: ui-monospace, monospace; font-size: 0.82rem; color: #93c5fd; }
  .next-steps { padding-left: 1.25rem; margin: 0; line-height: 1.9; font-size: 0.85rem; color: #d4d4d8; }
  .next-steps code { background: #0f0f12; padding: 0.15rem 0.4rem; border-radius: 3px;
                     font-family: ui-monospace, monospace; font-size: 0.8rem; color: #93c5fd; }
</style>
