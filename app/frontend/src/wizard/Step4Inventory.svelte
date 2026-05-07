<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import { wizardStore, addNode, removeNode, updateNode, type Role, type NodeSpec } from '../stores/wizard';
  import { api } from '../lib/api';

  const k8sRoles: Role[]  = ['control-plane', 'etcd', 'worker'];
  const cephRoles: Role[] = ['ceph-mon', 'ceph-mgr', 'ceph-osd', 'ceph-mds', 'ceph-rgw'];

  const topology = $derived($wizardStore.inventory.cluster.topology);
  const visibleRoles = $derived(
    topology === 'ceph-only' ? cephRoles
    : topology === 'k8s-only' ? k8sRoles
    : [...k8sRoles, ...cephRoles]
  );

  let validationResult = $state<{ valid: boolean; errors: string[] } | null>(null);
  let validating = $state(false);

  function nameserversInput(value: string) {
    wizardStore.update((s) => {
      s.inventory.network.nameservers = value.split(',').map((x) => x.trim()).filter(Boolean);
      return s;
    });
  }

  function toggleRole(idx: number, role: Role, checked: boolean) {
    const cur = $wizardStore.inventory.nodes[idx].roles;
    const next = checked ? [...cur, role] : cur.filter((r) => r !== role);
    updateNode(idx, { roles: next });
  }

  function addPreset(preset: 'k3s-1' | 'rke2-3' | 'ceph-3') {
    if (preset === 'k3s-1') {
      addNode({ hostname: 'k3s-server-01', ip: '10.10.1.31', roles: ['control-plane', 'etcd', 'worker'], os: 'microos' });
    }
    if (preset === 'rke2-3') {
      const base = 31;
      ['cp1', 'cp2', 'cp3'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`, roles: ['control-plane', 'etcd'], os: 'microos',
        cpu: 4, memory_gb: 8, disk_gb: 60
      }));
    }
    if (preset === 'ceph-3') {
      const base = 91;
      ['ceph-osd-01', 'ceph-osd-02', 'ceph-osd-03'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`,
        cluster_ip: `172.16.1.${base + i}`,
        roles: ['ceph-mon', 'ceph-mgr', 'ceph-osd'], os: 'leap',
        cpu: 4, memory_gb: 6, disk_gb: 64,
        storage_devices: ['/dev/sdb', '/dev/sdc']
      }));
    }
  }

  // Live YAML preview
  const yamlPreview = $derived.by(() => {
    const inv = $wizardStore.inventory;
    return `cluster:
  name: ${inv.cluster.name}
  domain: ${inv.cluster.domain}
  timezone: ${inv.cluster.timezone}
  kubernetes:
    distro: ${inv.cluster.kubernetes.distro}
    version: ${inv.cluster.kubernetes.version}
    cni: ${inv.cluster.kubernetes.cni}
network:
  pod_cidr: ${inv.network.pod_cidr}
  service_cidr: ${inv.network.service_cidr}
  vip: ${inv.network.vip}
  lb_pool: ${inv.network.lb_pool}
  ingress_lb_ip: ${inv.network.ingress_lb_ip}
  gateway: ${inv.network.gateway}
  nameservers: [${inv.network.nameservers.map((n) => `"${n}"`).join(', ')}]
target:
  type: ${inv.target.type}
  endpoint: ${inv.target.endpoint}
nodes:
${inv.nodes.map((n) => `  - hostname: ${n.hostname}
    ip: ${n.ip}${n.cluster_ip ? `\n    cluster_ip: ${n.cluster_ip}` : ''}
    roles: [${n.roles.join(', ')}]
    os: ${n.os}
    cpu: ${n.cpu}
    memory_gb: ${n.memory_gb}
    disk_gb: ${n.disk_gb}${n.storage_devices?.length ? `\n    storage_devices: [${n.storage_devices.map((d) => `"${d}"`).join(', ')}]` : ''}`).join('\n')}
ceph:
  mode: ${inv.ceph.mode}
  public_network: ${inv.ceph.public_network}
  cluster_network: ${inv.ceph.cluster_network}
  pools: [${inv.ceph.pools.join(', ')}]
addons:
  ingress: ${inv.addons.ingress}
  cert_manager: ${inv.addons.cert_manager}
  monitoring: ${inv.addons.monitoring}
  gitops: ${inv.addons.gitops}
content:
  ref: ${inv.content.ref}
`;
  });

  async function validate() {
    if (!$wizardStore.contentDir) {
      validationResult = { valid: false, errors: ['Content not fetched. Go back to step 1.'] };
      return;
    }
    validating = true;
    validationResult = await api.validateInventory(yamlPreview, $wizardStore.contentDir);
    validating = false;
  }

  // Allow Next freely. Validate is informational here; the real schema
  // gate runs in Step 5 right before terraform plan.
  const canAdvance = true;
  const nameserversText = $derived($wizardStore.inventory.network.nameservers.join(', '));
</script>

<header class="step-header">
  <h2>{$_('step.4.title')}</h2>
  <p>{$_('step.4.subtitle')}</p>
</header>

<div class="layout">
  <div class="form-col">
    <Section title={$_('step4.cluster')}>
      <div class="grid-2">
        <Field label={$_('step4.clusterName')} hint={$_('step4.clusterNameHint')} required>
          <input bind:value={$wizardStore.inventory.cluster.name} />
        </Field>
        <Field label={$_('step4.domain')} hint={$_('step4.domainHint')}>
          <input bind:value={$wizardStore.inventory.cluster.domain} />
        </Field>
        <Field label={$_('step4.timezone')}>
          <input bind:value={$wizardStore.inventory.cluster.timezone} />
        </Field>
        <Field label={$_('step4.k8sDistro')}>
          <select bind:value={$wizardStore.inventory.cluster.kubernetes.distro}>
            <option value="rke2">RKE2</option>
            <option value="k3s">K3s</option>
          </select>
        </Field>
        <Field label={$_('step4.k8sVersion')}>
          <input bind:value={$wizardStore.inventory.cluster.kubernetes.version} />
        </Field>
        <Field label={$_('step4.cni')}>
          <select bind:value={$wizardStore.inventory.cluster.kubernetes.cni}>
            <option value="cilium">Cilium</option>
            <option value="canal">Canal</option>
            <option value="calico">Calico</option>
          </select>
        </Field>
      </div>
    </Section>

    <Section title={$_('step4.network')}>
      <div class="grid-2">
        <Field label={$_('step4.podCIDR')}><input bind:value={$wizardStore.inventory.network.pod_cidr} /></Field>
        <Field label={$_('step4.serviceCIDR')}><input bind:value={$wizardStore.inventory.network.service_cidr} /></Field>
        <Field label={$_('step4.vip')} hint={$_('step4.vipHint')} required>
          <input bind:value={$wizardStore.inventory.network.vip} />
        </Field>
        <Field label={$_('step4.lbPool')} hint={$_('step4.lbPoolHint')}>
          <input bind:value={$wizardStore.inventory.network.lb_pool} />
        </Field>
        <Field label={$_('step4.ingressLBIP')}>
          <input bind:value={$wizardStore.inventory.network.ingress_lb_ip} />
        </Field>
        <Field label={$_('step4.gateway')}>
          <input bind:value={$wizardStore.inventory.network.gateway} />
        </Field>
        <Field label={$_('step4.nameservers')}>
          <input value={nameserversText} oninput={(e) => nameserversInput((e.target as HTMLInputElement).value)} />
        </Field>
      </div>
    </Section>

    <Section title={$_('step4.nodes')}
             subtitle={$wizardStore.inventory.nodes.length + ' nodes'}>
      <div class="presets">
        <span class="muted">Presets:</span>
        {#if topology !== 'ceph-only'}
          <Button onclick={() => addPreset('k3s-1')}>+ K3s 단일 노드</Button>
          <Button onclick={() => addPreset('rke2-3')}>+ RKE2 control-plane × 3</Button>
        {/if}
        {#if topology !== 'k8s-only'}
          <Button onclick={() => addPreset('ceph-3')}>+ Ceph OSD × 3</Button>
        {/if}
      </div>

      {#each $wizardStore.inventory.nodes as node, i}
        <div class="node-row">
          <div class="node-head">
            <input class="hostname"
                   value={node.hostname}
                   placeholder="hostname"
                   oninput={(e) => updateNode(i, { hostname: (e.target as HTMLInputElement).value })} />
            <Button variant="ghost" onclick={() => removeNode(i)}>✕</Button>
          </div>
          <div class="grid-3">
            <Field label="IP" required>
              <input value={node.ip}
                     oninput={(e) => updateNode(i, { ip: (e.target as HTMLInputElement).value })} />
            </Field>
            <Field label={$_('step4.node.clusterIP')} hint={$_('step4.node.clusterIPHint')}>
              <input value={node.cluster_ip ?? ''}
                     oninput={(e) => updateNode(i, { cluster_ip: (e.target as HTMLInputElement).value || undefined })} />
            </Field>
            <Field label="OS">
              <select value={node.os}
                      onchange={(e) => updateNode(i, { os: (e.target as HTMLSelectElement).value as NodeSpec['os'] })}>
                <option value="microos">MicroOS</option>
                <option value="leap">Leap 16</option>
                <option value="tumbleweed">Tumbleweed</option>
              </select>
            </Field>
            <Field label="CPU">
              <input type="number" min="1" value={node.cpu}
                     oninput={(e) => updateNode(i, { cpu: +(e.target as HTMLInputElement).value || 1 })} />
            </Field>
            <Field label={$_('step4.node.ram')}>
              <input type="number" min="1" value={node.memory_gb}
                     oninput={(e) => updateNode(i, { memory_gb: +(e.target as HTMLInputElement).value || 1 })} />
            </Field>
            <Field label={$_('step4.node.disk')}>
              <input type="number" min="10" value={node.disk_gb}
                     oninput={(e) => updateNode(i, { disk_gb: +(e.target as HTMLInputElement).value || 10 })} />
            </Field>
          </div>
          <Field label={$_('step4.node.roles')}>
            <div class="roles">
              {#each visibleRoles as r}
                <label class="role-chip" class:active={node.roles.includes(r)}>
                  <input type="checkbox"
                         checked={node.roles.includes(r)}
                         onchange={(e) => toggleRole(i, r, (e.target as HTMLInputElement).checked)} />
                  {r}
                </label>
              {/each}
            </div>
          </Field>
        </div>
      {/each}

      <div class="add-row">
        <Button variant="primary" onclick={() => addNode()}>+ {$_('step4.addNode')}</Button>
      </div>
    </Section>

    <div class="row">
      <Button variant="secondary" onclick={validate} disabled={validating}>
        {validating ? $_('common.loading') : $_('common.validate')}
      </Button>
      {#if validationResult?.valid}
        <Badge tone="success">{$_('step4.validationOk')}</Badge>
      {:else if validationResult && !validationResult.valid}
        <Badge tone="danger">{$_('step4.validationErr')}</Badge>
      {/if}
    </div>
    {#if validationResult && !validationResult.valid}
      <pre class="errors">{validationResult.errors.join('\n')}</pre>
    {/if}
  </div>

  <div class="preview-col">
    <Section title={$_('step4.preview')}>
      <pre class="yaml">{yamlPreview}</pre>
    </Section>
  </div>
</div>

<StepNav canAdvance={canAdvance} />

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }

  .layout { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: 1rem; }
  @media (max-width: 1100px) { .layout { grid-template-columns: 1fr; } }

  .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.5rem; }

  .node-row { padding: 0.75rem; background: #0f0f12; border: 1px solid #2a2a30;
              border-radius: 6px; margin-bottom: 0.6rem; }
  .node-head { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 0.5rem; }
  .hostname { flex: 1; background: #1b1b1f; color: #e4e4e7; border: 1px solid #3f3f46;
              border-radius: 5px; padding: 0.4rem 0.6rem; font-size: 0.9rem; font-weight: 500;
              font-family: inherit; outline: none; }
  .hostname:focus { border-color: #60a5fa; }

  .roles { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .role-chip { display: flex; gap: 0.3rem; align-items: center; padding: 0.2rem 0.55rem;
               background: #27272a; border: 1px solid #3f3f46; border-radius: 999px;
               font-size: 0.75rem; cursor: pointer; user-select: none; color: #a1a1aa; }
  .role-chip.active { background: #1e293b; border-color: #3b82f6; color: #93c5fd; }
  .role-chip input { display: none; }

  .add-row { margin-top: 0.5rem; }
  .presets { display: flex; gap: 0.4rem; align-items: center; flex-wrap: wrap; margin-bottom: 0.75rem; }
  .muted { color: #71717a; font-size: 0.8rem; }
  .row { display: flex; gap: 0.75rem; align-items: center; margin-top: 0.5rem; }

  .errors { background: #1f1f23; border: 1px solid #7f1d1d; color: #fca5a5;
            padding: 0.6rem; border-radius: 5px; font-size: 0.8rem;
            font-family: ui-monospace, monospace; white-space: pre-wrap; }

  .yaml { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
          border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.78rem;
          line-height: 1.5; max-height: 70vh; overflow: auto; color: #d4d4d8; margin: 0; }
</style>
