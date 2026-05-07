<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';

  type OS = 'microos' | 'leap' | 'tumbleweed';

  let k8sOS = $state<OS>('microos');
  let cephOS = $state<OS>('leap');

  const topology = $derived($wizardStore.inventory.cluster.topology);
  const showK8s  = $derived(topology === 'k8s-only'  || topology === 'combined');
  const showCeph = $derived(topology === 'ceph-only' || topology === 'combined');

  // ESXi today only supports MicroOS — Leap/Tumbleweed need Agama ISO
  // remaster (phase-1 §4) which isn't shipped. Surface the constraint
  // here so users don't pick Leap+ESXi and discover the gate at the
  // datastore_upload stage 5 minutes into the run.
  const isESXi = $derived($wizardStore.inventory.target.type === 'esxi');
  const esxiBlocked = $derived(isESXi && (k8sOS !== 'microos' || cephOS !== 'microos'));

  function chooseImage(role: 'k8s' | 'ceph', os: OS) {
    if (role === 'k8s') k8sOS = os;
    else cephOS = os;
    // Apply default OS to nodes by their roles when they exist (Step 4 lets users override).
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n) => {
          const isCeph = n.roles.some((r) => r.startsWith('ceph-'));
          return { ...n, os: isCeph ? cephOS : k8sOS };
        })
      }
    }));
  }

  const images: { id: OS; tag: string; descKey: string }[] = [
    { id: 'microos',    tag: 'MicroOS',    descKey: 'step3.image.microosDesc' },
    { id: 'leap',       tag: 'Leap 16',    descKey: 'step3.image.leapDesc' },
    { id: 'tumbleweed', tag: 'Tumbleweed', descKey: 'step3.image.tumbleweedDesc' }
  ];
</script>

<header class="step-header">
  <h2>{$_('step.3.title')}</h2>
  <p>{$_('step3.perRoleHint')}</p>
</header>

{#if esxiBlocked}
  <div class="warn">
    ⚠ {$_('step3.esxiOnlyMicroOS')}
  </div>
{/if}

{#if showK8s}
  <Section title={$_('step3.k8sNodes')}>
    <div class="image-grid">
      {#each images as img}
        <button class="image-card" class:active={k8sOS === img.id} onclick={() => chooseImage('k8s', img.id)}>
          <div class="head">
            <strong>{$_('step3.image.' + img.id)}</strong>
            {#if k8sOS === img.id}<Badge tone="info">{$_('common.apply')}</Badge>{/if}
          </div>
          <span>{$_(img.descKey)}</span>
        </button>
      {/each}
    </div>
  </Section>
{/if}

{#if showCeph}
  <Section title={$_('step3.cephNodes')}>
    <div class="image-grid">
      {#each images as img}
        <button class="image-card" class:active={cephOS === img.id} onclick={() => chooseImage('ceph', img.id)}>
          <div class="head">
            <strong>{$_('step3.image.' + img.id)}</strong>
            {#if cephOS === img.id}<Badge tone="info">{$_('common.apply')}</Badge>{/if}
          </div>
          <span>{$_(img.descKey)}</span>
        </button>
      {/each}
    </div>
  </Section>
{/if}

<StepNav canAdvance={true} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .image-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.75rem; }
  .image-card { display: flex; flex-direction: column; gap: 0.4rem; padding: 0.85rem 1rem;
                background: #0f0f12; border: 1px solid #2a2a30; border-radius: 6px;
                color: inherit; cursor: pointer; text-align: left; font-family: inherit; }
  .image-card:hover { border-color: #52525b; }
  .image-card.active { border-color: #3b82f6; background: #1e293b; }
  .image-card strong { font-size: 0.9rem; }
  .image-card span { font-size: 0.8rem; color: #a1a1aa; line-height: 1.4; }
  .head { display: flex; justify-content: space-between; align-items: center; }
  .warn { margin-bottom: 0.85rem; padding: 0.7rem 0.85rem; border-radius: 5px;
          background: #422006; border: 1px solid #92400e; color: #fbbf24;
          font-size: 0.82rem; line-height: 1.5; }
</style>
