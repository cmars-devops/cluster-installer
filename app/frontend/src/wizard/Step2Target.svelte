<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore } from '../stores/wizard';
  import { api, type ESXiDiscovery } from '../lib/api';

  type TargetType = 'libvirt' | 'proxmox' | 'esxi';

  const target = $derived($wizardStore.inventory.target);

  let testing = $state(false);
  let testResult = $state<{ ok: boolean; msg: string } | null>(null);

  let discovery = $state<ESXiDiscovery | null>(null);
  let manualDS = $state(false);
  let manualNet = $state(false);

  // ── Single immutable updater for the target sub-tree.
  // Using bind:value to deep store paths in Svelte 5 runes mode mutates in
  // place and does NOT propagate to derived values. Every input below uses
  // value={target.x} + oninput → updateTarget instead.
  function updateTarget(patch: Partial<typeof target>) {
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        target: { ...s.inventory.target, ...patch }
      }
    }));
  }

  function setType(t: TargetType) {
    const extra: Partial<typeof target> = {};
    if (t === 'esxi') {
      if (!target.username)     extra.username = 'root';
      if (!target.tls_insecure) extra.tls_insecure = true;
      if (!target.network)      extra.network = 'VM Network';
    }
    updateTarget({ type: t, ...extra });
    testResult = null;
    discovery = null;
  }

  async function testConnection() {
    testing = true; testResult = null;
    await new Promise((r) => setTimeout(r, 400));
    if (!target.endpoint) {
      testResult = { ok: false, msg: 'Endpoint required' };
    } else if (target.type === 'proxmox' && !target.api_token) {
      testResult = { ok: false, msg: 'Proxmox: API token required' };
    } else {
      testResult = { ok: true, msg: $_('step2.connOk') + ' (preview only)' };
    }
    testing = false;
  }

  async function discoverEsxi() {
    console.log('[discoverEsxi] click — endpoint:', target.endpoint, 'password set:', !!target.password);
    if (!target.endpoint || !target.password) {
      discovery = { ok: false, error: '엔드포인트와 root 패스워드 먼저 입력해주세요.' };
      return;
    }
    testing = true;
    discovery = null;
    const payload = {
      type: 'esxi',
      endpoint: target.endpoint,
      username: target.username || 'root',
      password: target.password,
      ssh_key: target.ssh_key,
      tls_insecure: target.tls_insecure
    };
    console.log('[discoverEsxi] calling api…', payload);
    let result: ESXiDiscovery;
    try {
      result = await api.discoverEsxi(payload);
      console.log('[discoverEsxi] api returned', result);
    } catch (e) {
      console.error('[discoverEsxi] api threw', e);
      result = { ok: false, error: String(e) };
    }
    discovery = result;
    // Surface discovered resources to other steps (Step 4 needs the
    // datastore list for per-node placement).
    if (result.ok) {
      wizardStore.update((s) => ({
        ...s,
        discovered: {
          host: result.host,
          datastores: result.datastores,
          networks: result.networks
        }
      }));
    }
    testing = false;
  }

  // Helpers for typed input handlers — Svelte 5 doesn't infer the
  // event.target type automatically inside template expressions.
  function val(e: Event): string {
    return (e.target as HTMLInputElement).value;
  }
  function checked(e: Event): boolean {
    return (e.target as HTMLInputElement).checked;
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

{#if target.type === 'libvirt'}
  <Section title={$_('step2.libvirt')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintLibvirt')} required>
      <input value={target.endpoint}
             oninput={(e) => updateTarget({ endpoint: val(e) })}
             placeholder="qemu+ssh://root@kvm1.local/system" />
    </Field>
    <Field label={$_('step2.sshKey')} hint={$_('step2.sshKeyHint')} required>
      <input value={target.ssh_key}
             oninput={(e) => updateTarget({ ssh_key: val(e) })}
             placeholder="C:\Users\you\.ssh\id_ed25519" />
    </Field>
    <div class="row">
      <Button variant="secondary" disabled={testing || !target.endpoint} onclick={testConnection}>
        {testing ? $_('common.loading') : $_('step2.testConn')}
      </Button>
      {#if testResult}<Badge tone={testResult.ok ? 'success' : 'danger'}>{testResult.msg}</Badge>{/if}
    </div>
  </Section>

{:else if target.type === 'proxmox'}
  <Section title={$_('step2.proxmox')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintProxmox')} required>
      <input value={target.endpoint}
             oninput={(e) => updateTarget({ endpoint: val(e) })}
             placeholder="https://pve1.example.com:8006/" />
    </Field>
    <Field label={$_('step2.apiToken')} hint={$_('step2.apiTokenHint')} required>
      <input type="password" value={target.api_token}
             oninput={(e) => updateTarget({ api_token: val(e) })}
             placeholder="root@pam!installer=…" />
    </Field>
    <label class="checkbox">
      <input type="checkbox" checked={target.tls_insecure}
             onchange={(e) => updateTarget({ tls_insecure: checked(e) })} />
      <span>{$_('step2.tlsInsecure')}</span>
    </label>
    <div class="row">
      <Button variant="secondary" disabled={testing || !target.endpoint} onclick={testConnection}>
        {testing ? $_('common.loading') : $_('step2.testConn')}
      </Button>
      {#if testResult}<Badge tone={testResult.ok ? 'success' : 'danger'}>{testResult.msg}</Badge>{/if}
    </div>
  </Section>

{:else if target.type === 'esxi'}
  <Section title={$_('step2.esxi')}>
    <Field label={$_('step2.endpoint')} hint={$_('step2.endpointHintEsxi')} required>
      <input value={target.endpoint}
             oninput={(e) => updateTarget({ endpoint: val(e) })}
             placeholder="https://192.168.1.210/" />
    </Field>
    <div class="grid-2">
      <Field label={$_('step2.username')} hint={$_('step2.usernameHintEsxi')} required>
        <input value={target.username}
               oninput={(e) => updateTarget({ username: val(e) })}
               placeholder="root" />
      </Field>
      <Field label={$_('step2.rootPassword')} hint={$_('step2.rootPasswordHint')} required>
        <input type="password" value={target.password}
               oninput={(e) => updateTarget({ password: val(e) })} />
      </Field>
    </div>
    <Field label={$_('step2.sshKey')} hint={$_('step2.sshKeyHintEsxi')}>
      <input value={target.ssh_key}
             oninput={(e) => updateTarget({ ssh_key: val(e) })}
             placeholder="(optional) C:\Users\you\.ssh\id_ed25519" />
    </Field>
    <label class="checkbox">
      <input type="checkbox" checked={target.tls_insecure}
             onchange={(e) => updateTarget({ tls_insecure: checked(e) })} />
      <span>{$_('step2.tlsInsecureEsxi')}</span>
    </label>

    <div class="row">
      <Button variant="primary"
              disabled={testing || !target.endpoint || !target.password}
              onclick={discoverEsxi}>
        {testing ? $_('step2.discovering') : $_('step2.discover')}
      </Button>
      {#if discovery && !discovery.ok}<Badge tone="danger">{discovery.error}</Badge>{/if}
    </div>
  </Section>

  {#if discovery?.ok && discovery.host}
    <Section title={$_('step2.discovered')}
             subtitle="{discovery.host.name} · ESXi {discovery.host.version} (build {discovery.host.build})">
      <div class="discovered-grid">
        <div class="metric">
          <span class="metric-label">{$_('step2.discoveredHost')}</span>
          <code>{discovery.host.name}</code>
          <span class="metric-sub">{discovery.host.api_type === 'VirtualCenter' ? 'vCenter' : 'standalone ESXi'}</span>
        </div>
        <div class="metric">
          <span class="metric-label">{$_('step2.discoveredVersion')}</span>
          <code>{discovery.host.version}</code>
          <span class="metric-sub">build {discovery.host.build}</span>
        </div>
        <div class="metric">
          <span class="metric-label">Datastores</span>
          <code>{discovery.datastores?.length ?? 0}</code>
          <span class="metric-sub">{(discovery.datastores ?? []).filter(d => d.accessible).length} accessible</span>
        </div>
        <div class="metric">
          <span class="metric-label">Networks</span>
          <code>{discovery.networks?.length ?? 0}</code>
          <span class="metric-sub">port groups</span>
        </div>
      </div>
    </Section>
  {/if}

  <Section title="ESXi 리소스 배치">
    {#if !discovery?.ok}
      <details class="help">
        <summary>💡 {$_('step2.datastoreHelpTitle')}</summary>
        <div class="help-body">
          <p>{$_('step2.datastoreHelpBody')}</p>
          <ol>
            <li><strong>{$_('step2.datastoreHelpStep1')}</strong></li>
            <li>{$_('step2.datastoreHelpStep2')}</li>
            <li>{$_('step2.datastoreHelpStep3')}</li>
          </ol>
        </div>
      </details>
    {/if}

    <Field label={$_('step2.datastore')} hint={discovery?.ok ? '' : $_('step2.datastoreHint')} required>
      {#if discovery?.ok && discovery.datastores && !manualDS}
        <select value={target.datastore}
                onchange={(e) => updateTarget({ datastore: val(e) })}>
          <option value="">— {$_('step2.datastorePicker')} —</option>
          {#each discovery.datastores.filter(d => d.accessible) as ds}
            <option value={ds.name}>
              {ds.name}  ({ds.type}, {ds.free_gb.toFixed(0)} / {ds.capacity_gb.toFixed(0)} GB)
            </option>
          {/each}
        </select>
        <button class="link" onclick={() => (manualDS = true)} type="button">{$_('step2.manualEntry')}</button>
      {:else}
        <input value={target.datastore}
               oninput={(e) => updateTarget({ datastore: val(e) })}
               placeholder="SSD-RAID0-4Ti-02" />
        {#if discovery?.ok}
          <button class="link" onclick={() => (manualDS = false)} type="button">← back to picker</button>
        {/if}
      {/if}
    </Field>

    <Field label={$_('step2.isoDatastore')} hint={$_('step2.isoDatastoreHint')}>
      {#if discovery?.ok && discovery.datastores}
        <select value={target.iso_datastore}
                onchange={(e) => updateTarget({ iso_datastore: val(e) })}>
          <option value="">(blank → same as above)</option>
          {#each discovery.datastores.filter(d => d.accessible) as ds}
            <option value={ds.name}>{ds.name}  ({ds.free_gb.toFixed(0)} GB free)</option>
          {/each}
        </select>
      {:else}
        <input value={target.iso_datastore}
               oninput={(e) => updateTarget({ iso_datastore: val(e) })}
               placeholder="(blank → same as above)" />
      {/if}
    </Field>

    <Field label={$_('step2.network')} hint={discovery?.ok ? '' : $_('step2.networkHint')} required>
      {#if discovery?.ok && discovery.networks && !manualNet}
        <select value={target.network}
                onchange={(e) => updateTarget({ network: val(e) })}>
          <option value="">— {$_('step2.networkPicker')} —</option>
          {#each discovery.networks as net}
            <option value={net.name}>
              {net.name}{net.vswitch ? `  (${net.vswitch}` : ''}{net.vlan_id ? `, VLAN ${net.vlan_id}` : ''}{net.vswitch ? ')' : ''}
            </option>
          {/each}
        </select>
        <button class="link" onclick={() => (manualNet = true)} type="button">{$_('step2.manualEntry')}</button>
      {:else}
        <input value={target.network}
               oninput={(e) => updateTarget({ network: val(e) })}
               placeholder="VM Network" />
        {#if discovery?.ok}
          <button class="link" onclick={() => (manualNet = false)} type="button">← back to picker</button>
        {/if}
      {/if}
    </Field>

    {#if !discovery?.ok}
      <p class="muted">데이터스토어/네트워크 이름이 정확하지 않을 수 있습니다 — 위쪽 "{$_('step2.discover')}" 버튼을 누르면 ESXi에서 실제 이름을 가져와 드롭다운으로 선택하실 수 있습니다.</p>
    {/if}
  </Section>

  <div class="warn">
    ⚠ Phase 1에서는 인벤토리만 캡처되고 govmomi 백엔드는 v2에서 활성화됩니다.
    Discovery 버튼은 dev 모드에서 mock 데이터를 반환합니다 (실제 ESXi 응답 형식과 동일).
  </div>
{/if}

<Section title="HTTP server">
  <Field label={$_('step2.advertiseIP')} hint={$_('step2.advertiseIPHint')}>
    <input value={target.advertise_ip}
           oninput={(e) => updateTarget({ advertise_ip: val(e) })}
           placeholder="(auto)" />
  </Field>
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
  .row { display: flex; gap: 0.75rem; align-items: center; flex-wrap: wrap; }
  .checkbox { display: flex; gap: 0.5rem; align-items: center; font-size: 0.85rem;
              color: #d4d4d8; cursor: pointer; }
  .checkbox input { accent-color: #3b82f6; }
  .muted { color: #71717a; font-size: 0.8rem; line-height: 1.5; margin: 0.5rem 0 0; }

  .discovered-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.75rem; }
  @media (max-width: 800px) { .discovered-grid { grid-template-columns: repeat(2, 1fr); } }
  .metric { display: flex; flex-direction: column; gap: 0.25rem;
            background: #0a0a0c; border: 1px solid #2a2a30;
            border-radius: 5px; padding: 0.6rem 0.8rem; }
  .metric-label { font-size: 0.7rem; color: #71717a; text-transform: uppercase; letter-spacing: 0.05em; }
  .metric code { background: transparent; padding: 0; color: #93c5fd; font-size: 0.95rem; font-weight: 500; }
  .metric-sub { font-size: 0.7rem; color: #a1a1aa; }

  .link { background: none; border: none; color: #60a5fa; cursor: pointer;
          font-size: 0.75rem; padding: 0.2rem 0; margin-left: 0.5rem;
          font-family: inherit; }
  .link:hover { text-decoration: underline; }

  .warn { margin: 0.5rem 0 1.25rem; padding: 0.6rem 0.8rem; background: #292524;
          border: 1px solid #78350f; border-radius: 5px; color: #fde68a; font-size: 0.78rem;
          line-height: 1.5; }

  .help { margin: 0 0 0.75rem; padding: 0; background: #0a0a0c;
          border: 1px solid #1e3a8a; border-radius: 5px; color: #cbd5e1; }
  .help summary { padding: 0.5rem 0.75rem; cursor: pointer; font-size: 0.82rem;
                  color: #93c5fd; user-select: none; list-style: none; }
  .help summary::-webkit-details-marker { display: none; }
  .help summary::before { content: '▶ '; transition: transform 0.15s; display: inline-block; }
  .help[open] summary::before { content: '▼ '; }
  .help-body { padding: 0 0.75rem 0.75rem; font-size: 0.78rem; line-height: 1.6; }
  .help-body p { margin: 0 0 0.5rem; }
  .help-body ol { margin: 0; padding-left: 1.25rem; }
  .help-body li { margin-bottom: 0.25rem; }
  .help-body strong { color: #93c5fd; }
</style>
