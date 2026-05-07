<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';

  type TargetType = 'libvirt' | 'proxmox' | 'esxi';

  const target = $derived($wizardStore.inventory.target);
  let testing = $state(false);
  let testResult = $state<{ ok: boolean; msg: string } | null>(null);

  function setType(t: TargetType) {
    wizardStore.update((s) => {
      s.inventory.target.type = t;
      // Provide sensible defaults per type so the form isn't empty.
      if (t === 'esxi' && !s.inventory.target.username) {
        s.inventory.target.username = 'root';
      }
      if (t === 'esxi' && !s.inventory.target.tls_insecure) {
        s.inventory.target.tls_insecure = true; // ESXi labs almost always self-signed
      }
      if (t === 'esxi' && !s.inventory.target.network) {
        s.inventory.target.network = 'VM Network';
      }
      return s;
    });
    testResult = null;
  }

  async function testConnection() {
    testing = true; testResult = null;
    // TODO(Phase 2): wire to a Go backend method that does the appropriate
    // probe per type (libvirt-go for libvirt URI, GET /api2/json/version for
    // Proxmox, govmomi NewClient for ESXi). For now we just sanity-check the
    // form filled-out state.
    await new Promise((r) => setTimeout(r, 600));
    if (!target.endpoint) {
      testResult = { ok: false, msg: 'Endpoint required' };
    } else if (target.type === 'esxi' && !target.password && !target.ssh_key) {
      testResult = { ok: false, msg: 'ESXi: provide either root password or SSH key' };
    } else if (target.type === 'proxmox' && !target.api_token) {
      testResult = { ok: false, msg: 'Proxmox: API token required' };
    } else {
      testResult = { ok: true, msg: $_('step2.connOk') + ' (preview only — backend probe lands in Phase 2)' };
    }
    testing = false;
  }

  const canAdvance = true;
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
    <button class="target-card" class:active={target.type === 'esxi'} onclick={() => setType('esxi')}>
      <strong>{$_('step2.esxi')}</strong>
      <span>{$_('step2.esxiDesc')}</span>
    </button>
  </div>
</Section>

<!-- ──────────── libvirt ──────────── -->
{#if target.type === 'libvirt'}
  <Section title={$_('step2.libvirt')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintLibvirt')} required>
      <input bind:value={$wizardStore.inventory.target.endpoint}
             placeholder="qemu+ssh://root@kvm1.local/system" />
    </Field>
    <Field label={$_('step2.sshKey')} hint={$_('step2.sshKeyHint')} required>
      <input bind:value={$wizardStore.inventory.target.ssh_key}
             placeholder="C:\Users\you\.ssh\id_ed25519" />
    </Field>
  </Section>

<!-- ──────────── proxmox ──────────── -->
{:else if target.type === 'proxmox'}
  <Section title={$_('step2.proxmox')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintProxmox')} required>
      <input bind:value={$wizardStore.inventory.target.endpoint}
             placeholder="https://pve1.example.com:8006/" />
    </Field>
    <Field label={$_('step2.apiToken')} hint={$_('step2.apiTokenHint')} required>
      <input bind:value={$wizardStore.inventory.target.api_token}
             type="password"
             placeholder="root@pam!installer=…" />
    </Field>
    <label class="checkbox">
      <input type="checkbox" bind:checked={$wizardStore.inventory.target.tls_insecure} />
      <span>{$_('step2.tlsInsecure')}</span>
    </label>
  </Section>

<!-- ──────────── esxi ──────────── -->
{:else if target.type === 'esxi'}
  <Section title={$_('step2.esxi')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintEsxi')} required>
      <input bind:value={$wizardStore.inventory.target.endpoint}
             placeholder="https://192.168.1.210/" />
    </Field>
    <div class="grid-2">
      <Field label={$_('step2.username')} hint={$_('step2.usernameHintEsxi')} required>
        <input bind:value={$wizardStore.inventory.target.username} placeholder="root" />
      </Field>
      <Field label={$_('step2.rootPassword')} hint={$_('step2.rootPasswordHint')} required>
        <input bind:value={$wizardStore.inventory.target.password} type="password" />
      </Field>
    </div>
    <Field label={$_('step2.sshKey')} hint={$_('step2.sshKeyHintEsxi')}>
      <input bind:value={$wizardStore.inventory.target.ssh_key}
             placeholder="(optional) C:\Users\you\.ssh\id_ed25519" />
    </Field>

    <h4 class="subhead">ESXi 리소스 배치</h4>
    <div class="grid-2">
      <Field label={$_('step2.datastore')} hint={$_('step2.datastoreHint')} required>
        <input bind:value={$wizardStore.inventory.target.datastore}
               placeholder="SSD-RAID0-4Ti-02" />
      </Field>
      <Field label={$_('step2.isoDatastore')} hint={$_('step2.isoDatastoreHint')}>
        <input bind:value={$wizardStore.inventory.target.iso_datastore}
               placeholder="(blank → same as above)" />
      </Field>
    </div>
    <Field label={$_('step2.network')} hint={$_('step2.networkHint')} required>
      <input bind:value={$wizardStore.inventory.target.network} placeholder="VM Network" />
    </Field>

    <label class="checkbox">
      <input type="checkbox" bind:checked={$wizardStore.inventory.target.tls_insecure} />
      <span>{$_('step2.tlsInsecureEsxi')}</span>
    </label>

    <div class="warn">
      ⚠ Phase 1 v1 타겟은 libvirt + Proxmox 우선입니다. ESXi 백엔드(govmomi 어댑터)는
      v2에서 활성화됩니다 — 인벤토리는 지금 저장되지만, Apply 시점에 명확한 안내와 함께
      중단됩니다. 자세히는 <code>docs/phase-1-open-items.md</code>.
    </div>
  </Section>
{/if}

<!-- ──────────── shared ──────────── -->
<Section title="HTTP server">
  <Field label={$_('step2.advertiseIP')} hint={$_('step2.advertiseIPHint')}>
    <input bind:value={$wizardStore.inventory.target.advertise_ip} placeholder="(auto)" />
  </Field>

  <div class="row">
    <Button variant="secondary" disabled={testing} onclick={testConnection}>
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

  .targets { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.75rem; }
  @media (max-width: 900px) { .targets { grid-template-columns: 1fr; } }
  .target-card { display: flex; flex-direction: column; gap: 0.3rem; align-items: flex-start;
                 padding: 1rem; border-radius: 6px; cursor: pointer;
                 background: #0f0f12; border: 1px solid #2a2a30; color: inherit;
                 text-align: left; font-family: inherit; transition: border-color 0.1s; }
  .target-card:hover { border-color: #52525b; }
  .target-card.active { border-color: #3b82f6; background: #1e293b; }
  .target-card strong { font-size: 0.9rem; }
  .target-card span { font-size: 0.78rem; color: #a1a1aa; line-height: 1.4; }

  .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .subhead { font-size: 0.8rem; color: #a1a1aa; font-weight: 500; margin: 0.5rem 0 0;
             text-transform: uppercase; letter-spacing: 0.05em; }
  .row { display: flex; gap: 0.75rem; align-items: center; }
  .checkbox { display: flex; gap: 0.5rem; align-items: center; font-size: 0.85rem;
              color: #d4d4d8; cursor: pointer; }
  .checkbox input { accent-color: #3b82f6; }
  .warn { margin-top: 0.5rem; padding: 0.6rem 0.8rem; background: #292524;
          border: 1px solid #78350f; border-radius: 5px; color: #fde68a; font-size: 0.78rem;
          line-height: 1.5; }
  .warn code { background: #44403c; padding: 0.05rem 0.3rem; border-radius: 3px;
               font-family: ui-monospace, monospace; }
</style>
