<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';

  const target = $derived($wizardStore.inventory.target);
  let testing = $state(false);
  let testResult = $state<{ ok: boolean; msg: string } | null>(null);

  function setType(t: 'libvirt' | 'proxmox') {
    wizardStore.update((s) => { s.inventory.target.type = t; return s; });
  }

  async function testConnection() {
    testing = true; testResult = null;
    // TODO: wire to a real Go backend method that runs an SSH dial or HTTP probe.
    // For now we just mock the round-trip.
    await new Promise((r) => setTimeout(r, 600));
    if (target.endpoint) {
      testResult = { ok: true, msg: $_('step2.connOk') };
    } else {
      testResult = { ok: false, msg: $_('step2.connFailed') };
    }
    testing = false;
  }

  const canAdvance = $derived(!!target.endpoint);
</script>

<header class="step-header">
  <h2>{$_('step.2.title')}</h2>
  <p>{$_('step.2.subtitle')}</p>
</header>

<Section title={$_('step2.type')}>
  <div class="targets">
    <button class="target-card" class:active={target.type === 'libvirt'} onclick={() => setType('libvirt')}>
      <strong>{$_('step2.libvirt')}</strong>
      <span>{$_('step2.libvirtDesc')}</span>
    </button>
    <button class="target-card" class:active={target.type === 'proxmox'} onclick={() => setType('proxmox')}>
      <strong>{$_('step2.proxmox')}</strong>
      <span>{$_('step2.proxmoxDesc')}</span>
    </button>
  </div>
</Section>

<Section title={target.type === 'libvirt' ? $_('step2.libvirt') : $_('step2.proxmox')}>
  <Field label={$_('step2.endpoint')}
         hint={target.type === 'libvirt' ? $_('step2.endpointHintLibvirt') : $_('step2.endpointHintProxmox')}
         required>
    <input bind:value={$wizardStore.inventory.target.endpoint}
           placeholder={target.type === 'libvirt' ? 'qemu+ssh://root@kvm1.local/system' : 'https://pve1.example.com:8006/'} />
  </Field>

  {#if target.type === 'libvirt'}
    <Field label={$_('step2.sshKey')} hint={$_('step2.sshKeyHint')}>
      <input bind:value={$wizardStore.inventory.target.ssh_key} placeholder="C:\Users\you\.ssh\id_ed25519" />
    </Field>
  {:else}
    <Field label={$_('step2.apiToken')} hint={$_('step2.apiTokenHint')}>
      <input bind:value={$wizardStore.inventory.target.api_token} type="password" placeholder="root@pam!installer=…" />
    </Field>
    <label class="checkbox">
      <input type="checkbox" bind:checked={$wizardStore.inventory.target.tls_insecure} />
      <span>{$_('step2.tlsInsecure')}</span>
    </label>
  {/if}

  <Field label={$_('step2.advertiseIP')} hint={$_('step2.advertiseIPHint')}>
    <input bind:value={$wizardStore.inventory.target.advertise_ip} placeholder="(auto)" />
  </Field>

  <div class="row">
    <Button variant="secondary" disabled={!target.endpoint || testing} onclick={testConnection}>
      {testing ? $_('common.loading') : $_('step2.testConn')}
    </Button>
    {#if testResult}
      <Badge tone={testResult.ok ? 'success' : 'danger'}>{testResult.msg}</Badge>
    {/if}
  </div>
</Section>

<StepNav canAdvance={canAdvance} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }
  .targets { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .target-card { display: flex; flex-direction: column; gap: 0.3rem; align-items: flex-start;
                 padding: 1rem; border-radius: 6px; cursor: pointer;
                 background: #0f0f12; border: 1px solid #2a2a30; color: inherit;
                 text-align: left; font-family: inherit; transition: border-color 0.1s; }
  .target-card:hover { border-color: #52525b; }
  .target-card.active { border-color: #3b82f6; background: #1e293b; }
  .target-card strong { font-size: 0.9rem; }
  .target-card span { font-size: 0.8rem; color: #a1a1aa; }
  .row { display: flex; gap: 0.75rem; align-items: center; }
  .checkbox { display: flex; gap: 0.5rem; align-items: center; font-size: 0.85rem; color: #d4d4d8; cursor: pointer; }
  .checkbox input { accent-color: #3b82f6; }
</style>
