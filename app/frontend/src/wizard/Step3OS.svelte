<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';

  type OS = 'microos' | 'leap' | 'tumbleweed' | 'ubuntu';

  const topology = $derived($wizardStore.inventory.cluster.topology);
  // dev-vm flow is intentionally minimal: single OS, single version. No
  // role grids, no cluster gating — just "what should this one VM run?".
  const devVMMode = $derived(topology === 'dev-vm');
  const showK8s   = $derived(topology === 'k8s-only'  || topology === 'combined');
  const showCeph  = $derived(topology === 'ceph-only' || topology === 'combined');

  // Cluster-mode: Step 4 has roles → per-role OS preference.
  const k8sOS  = $derived<OS>($wizardStore.osPreferences.k8s);
  const cephOS = $derived<OS>($wizardStore.osPreferences.ceph);

  function chooseImage(role: 'k8s' | 'ceph', os: OS) {
    wizardStore.update((s) => ({
      ...s,
      osPreferences: { ...s.osPreferences, [role]: os },
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n) => {
          const isCeph = n.roles.some((r) => r.startsWith('ceph-'));
          if (role === 'k8s' && !isCeph) return { ...n, os };
          if (role === 'ceph' && isCeph) return { ...n, os };
          return n;
        })
      }
    }));
  }

  // dev-vm: single node, single OS+version. State lives on nodes[0].
  const devVMNode = $derived(devVMMode ? $wizardStore.inventory.nodes[0] : undefined);
  const devVMOS = $derived<OS>(devVMNode?.os ?? 'ubuntu');
  const devVMVersion = $derived(devVMNode?.os_version ?? '26.04');

  function chooseDevVMOS(os: OS, version: string) {
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) =>
          i === 0 ? { ...n, os, os_version: version } : n
        )
      }
    }));
  }

  // Cluster-mode catalog — used by k8s/ceph branches only.
  const clusterImages: { id: OS; descKey: string }[] = [
    { id: 'microos',    descKey: 'step3.image.microosDesc' },
    { id: 'leap',       descKey: 'step3.image.leapDesc' },
    { id: 'tumbleweed', descKey: 'step3.image.tumbleweedDesc' },
    { id: 'ubuntu',     descKey: 'step3.image.ubuntuDesc' }
  ];

  // dev-vm catalog: Ubuntu LTS only for v1. Adding more distros (Leap,
  // RHEL family, etc.) becomes a routine catalog edit once the single-VM
  // flow is solid.
  const devVMImages: { id: OS; version: string; label: string; descKey: string; recommended?: boolean }[] = [
    { id: 'ubuntu', version: '26.04', label: 'Ubuntu 26.04 LTS', descKey: 'step3.image.ubuntu26Desc', recommended: true },
    { id: 'ubuntu', version: '24.04', label: 'Ubuntu 24.04 LTS', descKey: 'step3.image.ubuntu24Desc' }
  ];
</script>

<header class="step-header">
  <h2>{$_('step.3.title')}</h2>
  {#if devVMMode}
    <p>{$_('step3.devVMHint')}</p>
  {:else}
    <p>{$_('step3.perRoleHint')}</p>
  {/if}
</header>

{#if devVMMode}
  <Section title={$_('step3.devVMSectionTitle')}>
    <div class="image-grid devvm">
      {#each devVMImages as img}
        {@const active = devVMOS === img.id && devVMVersion === img.version}
        <button class="image-card" class:active onclick={() => chooseDevVMOS(img.id, img.version)}>
          <div class="head">
            <strong>{img.label}</strong>
            <div class="badges">
              {#if img.recommended}<Badge tone="success">{$_('step3.recommended')}</Badge>{/if}
              {#if active}<Badge tone="info">선택됨</Badge>{/if}
            </div>
          </div>
          <span>{$_(img.descKey)}</span>
        </button>
      {/each}
    </div>
    <p class="muted">{$_('step3.devVMOnlyUbuntuNote')}</p>
  </Section>
{:else}
  {#if showK8s}
    <Section title={$_('step3.k8sNodes')}>
      <div class="image-grid">
        {#each clusterImages as img}
          <button class="image-card" class:active={k8sOS === img.id} onclick={() => chooseImage('k8s', img.id)}>
            <div class="head">
              <strong>{$_('step3.image.' + img.id)}</strong>
              {#if k8sOS === img.id}<Badge tone="info">선택됨</Badge>{/if}
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
        {#each clusterImages as img}
          <button class="image-card" class:active={cephOS === img.id} onclick={() => chooseImage('ceph', img.id)}>
            <div class="head">
              <strong>{$_('step3.image.' + img.id)}</strong>
              {#if cephOS === img.id}<Badge tone="info">선택됨</Badge>{/if}
            </div>
            <span>{$_(img.descKey)}</span>
          </button>
        {/each}
      </div>
    </Section>
  {/if}
{/if}

<StepNav canAdvance={true} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }

  .image-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.75rem; }
  .image-grid.devvm { grid-template-columns: repeat(2, 1fr); }
  @media (max-width: 1100px) { .image-grid { grid-template-columns: repeat(2, 1fr); } }
  @media (max-width: 700px) { .image-grid { grid-template-columns: 1fr; }
                              .image-grid.devvm { grid-template-columns: 1fr; } }

  .image-card { display: flex; flex-direction: column; gap: 0.4rem; padding: 0.85rem 1rem;
                background: #0f0f12; border: 1px solid #2a2a30; border-radius: 6px;
                color: inherit; cursor: pointer; text-align: left; font-family: inherit;
                transition: border-color 0.1s, background-color 0.1s, transform 0.05s; }
  .image-card:hover { border-color: #52525b; background: #16161a; }
  .image-card:active { transform: scale(0.985); }
  .image-card.active { border-color: #3b82f6; background: #1e293b;
                       box-shadow: 0 0 0 1px #3b82f6 inset; }
  .image-card strong { font-size: 0.9rem; }
  .image-card span { font-size: 0.78rem; color: #a1a1aa; line-height: 1.45; }
  .head { display: flex; justify-content: space-between; align-items: center; gap: 0.5rem; }
  .badges { display: flex; gap: 0.3rem; }

  .muted { color: #71717a; font-size: 0.78rem; margin: 0.6rem 0 0; }
  .image-card * { pointer-events: none; }
</style>
