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
  const showK8s   = $derived(topology === 'k8s-only'  || topology === 'combined');
  const showCeph  = $derived(topology === 'ceph-only' || topology === 'combined');
  const visibleRoles = $derived(
    topology === 'ceph-only' ? cephRoles
    : topology === 'k8s-only' ? k8sRoles
    : [...k8sRoles, ...cephRoles]
  );

  let validationResult = $state<{ valid: boolean; errors: string[] } | null>(null);
  let validating = $state(false);

  // ── Ceph-specific updaters ────────────────────────────────────────────
  function updateCeph(patch: Partial<typeof $wizardStore.inventory.ceph>) {
    wizardStore.update((s) => ({
      ...s,
      inventory: { ...s.inventory, ceph: { ...s.inventory.ceph, ...patch } }
    }));
  }
  function togglePool(pool: 'rbd' | 'cephfs' | 'rgw', on: boolean) {
    const cur = $wizardStore.inventory.ceph.pools;
    const next = on ? Array.from(new Set([...cur, pool])) : cur.filter(p => p !== pool);
    updateCeph({ pools: next });
  }

  // ── Discovered datastores (from Step 2 ESXi/Proxmox discovery) ────────
  const datastoreOptions = $derived(
    ($wizardStore.discovered.datastores ?? [])
      .filter((d) => d.accessible !== false)
  );

  // ── Computed Ceph health hints (live) ─────────────────────────────────
  const monCount = $derived($wizardStore.inventory.nodes.filter(n => n.roles.includes('ceph-mon')).length);
  const osdNodes = $derived($wizardStore.inventory.nodes.filter(n => n.roles.includes('ceph-osd')));
  const osdNodesWithoutDevices = $derived(osdNodes.filter(n => dataDevicesOf(n).length === 0));
  const hddOSDsWithoutDB = $derived(osdNodes.filter(n => {
    const cls = n.device_class ?? 'auto';
    const hasDB = (n.db_devices ?? []).length > 0;
    return (cls === 'hdd') && !hasDB && dataDevicesOf(n).length > 0;
  }));

  // Detect risky datastore concentration (same physical array hosting too
  // many quorum-critical nodes — single failure = split brain).
  const datastoreUsage = $derived.by(() => {
    const counts: Record<string, { mons: number; osds: number; total: number }> = {};
    for (const n of $wizardStore.inventory.nodes) {
      const ds = n.datastore || $wizardStore.inventory.target.datastore || '<default>';
      counts[ds] ??= { mons: 0, osds: 0, total: 0 };
      counts[ds].total++;
      if (n.roles.includes('ceph-mon')) counts[ds].mons++;
      if (n.roles.includes('ceph-osd')) counts[ds].osds++;
    }
    return counts;
  });
  const datastoreHints = $derived.by(() => {
    if (!showCeph) return [] as { tone: 'warn'; msg: string }[];
    const out: { tone: 'warn'; msg: string }[] = [];
    for (const [ds, c] of Object.entries(datastoreUsage)) {
      // Quorum risk: 2+ mons on same datastore — losing that array kills
      // quorum even if other nodes are healthy.
      if (c.mons >= 2 && monCount >= 3) {
        out.push({ tone: 'warn',
          msg: `데이터스토어 '${ds}'에 mon 노드가 ${c.mons}개 — 같은 어레이 장애 시 quorum 손실 위험. 다른 데이터스토어로 분산 권장.` });
      }
      // OSD concentration: more than half OSDs on same datastore.
      if (c.osds >= 3 && osdNodes.length > 0 && c.osds > osdNodes.length / 2) {
        out.push({ tone: 'warn',
          msg: `데이터스토어 '${ds}'에 OSD ${c.osds}/${osdNodes.length}개 집중 — failure domain 분산 권장.` });
      }
    }
    return out;
  });
  const cephHints = $derived.by(() => {
    if (!showCeph) return [];
    const out: { tone: 'warn' | 'danger' | 'info'; msg: string }[] = [];
    if (monCount === 0) {
      out.push({ tone: 'danger', msg: `Mon 노드가 없습니다. quorum을 위해 최소 1개(권장 3개) 필요합니다.` });
    } else if (monCount % 2 === 0) {
      out.push({ tone: 'warn', msg: `Mon 노드 ${monCount}개 — 짝수는 split-brain 위험. 1개 추가하거나 제거해서 홀수로 맞추세요.` });
    } else if (monCount === 1) {
      out.push({ tone: 'warn', msg: `Mon 노드 1개 — 단일 장애점입니다. 운영 환경은 3개 권장.` });
    }
    if (osdNodes.length === 0) {
      out.push({ tone: 'danger', msg: `OSD 노드가 없습니다. 데이터 저장 불가. 최소 3개 권장 (replica 3 시).` });
    } else if (osdNodes.length < 3) {
      out.push({ tone: 'warn', msg: `OSD 노드 ${osdNodes.length}개 — 기본 replica 3에 미달. 풀 size를 ${osdNodes.length}로 낮추거나 노드 추가.` });
    }
    if (osdNodesWithoutDevices.length > 0) {
      out.push({ tone: 'danger',
        msg: `OSD 노드 ${osdNodesWithoutDevices.length}개에 Data devices 미설정 — cephadm이 디스크를 찾지 못합니다. 각 OSD 행의 'Data devices' 필드에 /dev/sdb 형식으로 입력.` });
    }
    if (hddOSDsWithoutDB.length > 0) {
      out.push({ tone: 'warn',
        msg: `HDD OSD 노드 ${hddOSDsWithoutDB.length}개가 BlueStore DB를 분리하지 않음 — DB/WAL을 SSD로 빼면 throughput 4-8× 개선. 각 OSD 행 '고급 OSD 옵션'의 'DB/WAL devices'에 SSD 경로 입력 권장.` });
    }
    const replication = $wizardStore.inventory.ceph.replication ?? 3;
    if (osdNodes.length > 0 && replication > osdNodes.length) {
      out.push({ tone: 'danger',
        msg: `Replica ${replication} 설정인데 OSD 노드 ${osdNodes.length}개 — Ceph는 같은 host에 동일 PG 복제를 두지 않으므로 PG가 'undersized'/'degraded' 상태가 됩니다. 노드를 늘리거나 'OSD 기본값' 섹션의 복제 수를 ${osdNodes.length} 이하로 낮추세요.` });
    }
    const thinOSDs = osdNodes.filter(n => (n.disk_provisioning ?? 'thin') === 'thin').length;
    if (thinOSDs > 0) {
      out.push({ tone: 'warn',
        msg: `OSD 노드 ${thinOSDs}개가 thin 프로비저닝 — first-write 시 할당으로 OSD throughput 저하. ESXi라면 'thick eager-zeroed', libvirt/Proxmox라면 'thick' 권장.` });
    }
    return out;
  });

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

  // ── Preset definitions — each describes a role family with sensible
  //    defaults that operators can scale via the count combobox.
  type PresetKind =
    | 'k3s-single' | 'rke2-cp' | 'rke2-worker'
    | 'ceph-core' | 'ceph-osd' | 'ceph-rgw';

  type PresetSpec = {
    kind: PresetKind;
    color: string;                      // accent color for the preset card
    label: string;
    badge: string;                      // role chips summary
    description: string;
    hostnamePrefix: string;             // e.g. "ceph-osd"
    ipBase: number;                     // last octet base, e.g. 91
    cidr3: string;                      // first 3 octets
    clusterIPBase?: number;             // optional Ceph cluster network IP
    clusterCidr3?: string;
    defaultCount: number;
    countOptions: number[];             // 1..N suggestions for the combobox
    template: Partial<NodeSpec>;        // default fields for each generated node
  };

  const PRESETS: PresetSpec[] = [
    {
      kind: 'k3s-single',
      color: '#3b82f6',
      label: 'K3s 단일 노드',
      badge: 'control-plane · etcd · worker',
      description: '모든 역할을 한 노드에 — 홈랩 / 빠른 검증',
      hostnamePrefix: 'k3s',
      ipBase: 31, cidr3: '10.10.1',
      defaultCount: 1, countOptions: [1],
      template: { roles: ['control-plane', 'etcd', 'worker'], os: 'microos',
                  cpu: 4, memory_gb: 8, disk_gb: 60 }
    },
    {
      kind: 'rke2-cp',
      color: '#3b82f6',
      label: 'RKE2 control-plane',
      badge: 'control-plane · etcd',
      description: 'API 서버 + etcd 멤버. HA는 3개 (홀수만 의미 있음).',
      hostnamePrefix: 'cp',
      ipBase: 31, cidr3: '10.10.1',
      defaultCount: 3, countOptions: [1, 3, 5],
      template: { roles: ['control-plane', 'etcd'], os: 'microos',
                  cpu: 4, memory_gb: 8, disk_gb: 60 }
    },
    {
      kind: 'rke2-worker',
      color: '#3b82f6',
      label: 'RKE2 worker',
      badge: 'worker',
      description: '워크로드 실행 노드. CPU/RAM은 사용 시나리오에 맞춰 키우세요.',
      hostnamePrefix: 'worker',
      ipBase: 41, cidr3: '10.10.1',
      defaultCount: 3, countOptions: [1, 2, 3, 4, 5, 6, 8, 10],
      template: { roles: ['worker'], os: 'microos',
                  cpu: 8, memory_gb: 16, disk_gb: 120 }
    },
    {
      kind: 'ceph-core',
      color: '#f59e0b',
      label: 'Ceph CORE',
      badge: 'mon · mgr · mds',
      description: 'Ceph 컨트롤 플레인 (3개 권장 — quorum 위해 홀수).',
      hostnamePrefix: 'ceph-core',
      ipBase: 75, cidr3: '10.10.1',
      defaultCount: 3, countOptions: [1, 3, 5],
      template: { roles: ['ceph-mon', 'ceph-mgr', 'ceph-mds'], os: 'leap',
                  cpu: 4, memory_gb: 8, disk_gb: 64 }
    },
    {
      kind: 'ceph-osd',
      color: '#f59e0b',
      label: 'Ceph OSD',
      badge: 'osd · HDD + DB on SSD',
      description: '데이터 보관. 노드당 HDD(/dev/sdb) + SSD(/dev/sdc for BlueStore DB). 노드별 디스크는 펼침에서 조정.',
      hostnamePrefix: 'ceph-osd',
      ipBase: 91, cidr3: '10.10.1',
      clusterIPBase: 91, clusterCidr3: '172.16.1',     // OSD 백엔드 복제 네트워크
      defaultCount: 3, countOptions: [3, 4, 5, 6, 7, 8, 10, 12],
      template: {
        roles: ['ceph-osd'], os: 'leap',
        cpu: 4, memory_gb: 6, disk_gb: 64,
        data_devices: ['/dev/sdb'],
        db_devices:   ['/dev/sdc'],
        device_class: 'hdd',
        osds_per_device: 1,
        osd_encrypted: false,
        disk_provisioning: 'thick-eager'   // OSD: avoid first-write penalty
      }
    },
    {
      kind: 'ceph-rgw',
      color: '#f59e0b',
      label: 'Ceph RGW',
      badge: 'rgw · S3',
      description: 'S3 호환 오브젝트 게이트웨이. HA는 2개 + 외부 LB(keepalived).',
      hostnamePrefix: 'ceph-rgw',
      ipBase: 81, cidr3: '10.10.1',
      defaultCount: 2, countOptions: [1, 2, 3, 4],
      template: { roles: ['ceph-rgw'], os: 'leap',
                  cpu: 2, memory_gb: 4, disk_gb: 64 }
    }
  ];

  // Per-preset count selection (UI state, not in store).
  let presetCount = $state<Record<PresetKind, number>>(
    Object.fromEntries(PRESETS.map(p => [p.kind, p.defaultCount])) as Record<PresetKind, number>
  );

  // Filter presets by topology so ceph-only mode hides K8s cards, etc.
  const visiblePresets = $derived(PRESETS.filter(p => {
    const isCeph = p.kind.startsWith('ceph-');
    if (topology === 'ceph-only') return isCeph;
    if (topology === 'k8s-only')  return !isCeph;
    return true;
  }));

  function nextHostnameIndex(prefix: string): number {
    const re = new RegExp(`^${prefix}-(\\d+)$`);
    let max = 0;
    for (const n of $wizardStore.inventory.nodes) {
      const m = n.hostname.match(re);
      if (m) max = Math.max(max, parseInt(m[1], 10));
    }
    return max + 1;
  }

  // ── Apply preset N times. New nodes pick up where existing same-prefix
  //    nodes left off (ceph-osd-04, -05 if -01..-03 already exist), and the
  //    last octet of the IP follows the same numbering so addresses stay
  //    aligned with hostnames.
  function addPreset(kind: PresetKind, count: number) {
    const p = PRESETS.find(x => x.kind === kind);
    if (!p) return;
    let startIdx = nextHostnameIndex(p.hostnamePrefix);
    // For k3s-single (single-node) and any preset whose first add-batch is
    // expected to use 'NN-01', ensure we always start at 01 if no existing.
    for (let n = 0; n < count; n++) {
      const idx = startIdx + n;
      const idxStr = String(idx).padStart(2, '0');
      const lastOct = p.ipBase + idx - 1;
      const node: Partial<NodeSpec> = {
        ...p.template,
        hostname: p.kind === 'k3s-single' && idx === 1
          ? 'k3s-server-01'
          : `${p.hostnamePrefix}-${idxStr}`,
        ip: `${p.cidr3}.${lastOct}`
      };
      if (p.clusterIPBase && p.clusterCidr3) {
        node.cluster_ip = `${p.clusterCidr3}.${p.clusterIPBase + idx - 1}`;
      }
      addNode(node);
    }
  }

  // ── OSD device helpers ────────────────────────────────────────────────
  // data_devices is the canonical field; storage_devices is kept as a
  // legacy alias so older inventories still load. We always read both
  // when emitting, but only WRITE data_devices on edit.
  function dataDevicesOf(n: NodeSpec): string[] {
    return n.data_devices ?? n.storage_devices ?? [];
  }
  function devicesText(arr: string[] | undefined): string {
    return (arr ?? []).join(', ');
  }
  function parseDevices(text: string): string[] {
    return text.split(',').map(s => s.trim()).filter(Boolean);
  }
  function setDataDevices(idx: number, text: string) {
    updateNode(idx, { data_devices: parseDevices(text), storage_devices: undefined });
  }
  function setDBDevices(idx: number, text: string) {
    const list = parseDevices(text);
    updateNode(idx, { db_devices: list.length > 0 ? list : undefined });
  }

  function updateCephDefaults(patch: Partial<typeof $wizardStore.inventory.ceph>) {
    updateCeph(patch);
  }

  // Toggle the per-node "advanced OSD options" panel. Tracked outside the
  // store because it's purely UI state.
  let advancedOSD = $state<Record<number, boolean>>({});
  function toggleAdvanced(idx: number) {
    advancedOSD = { ...advancedOSD, [idx]: !advancedOSD[idx] };
  }

  // Per-node row expand/collapse. Default collapsed; "+ 노드 추가" expands
  // the new row so the operator can fill it in. Preset-added rows stay
  // collapsed because their defaults are already sensible.
  let nodeExpanded = $state<Record<number, boolean>>({});
  function toggleNode(idx: number) {
    nodeExpanded = { ...nodeExpanded, [idx]: !nodeExpanded[idx] };
  }
  function expandAllNodes() {
    const next: Record<number, boolean> = {};
    $wizardStore.inventory.nodes.forEach((_, i) => (next[i] = true));
    nodeExpanded = next;
  }
  function collapseAllNodes() {
    nodeExpanded = {};
  }
  // Used to auto-expand the row created by the "+ 노드 추가" button.
  function addNodeManual() {
    const newIdx = $wizardStore.inventory.nodes.length;
    addNode();
    nodeExpanded = { ...nodeExpanded, [newIdx]: true };
  }

  // Concise per-node validation summary surfaced on the collapsed row.
  function rowIssues(n: NodeSpec): string[] {
    const errs: string[] = [];
    if (!n.ip) errs.push('IP 누락');
    if (n.roles.length === 0) errs.push('역할 없음');
    if (n.roles.includes('ceph-osd') && dataDevicesOf(n).length === 0) errs.push('Data devices 누락');
    return errs;
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
        {#if showK8s}
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
        {/if}
      </div>
    </Section>

    <Section title={$_('step4.network')}>
      <div class="grid-2">
        {#if showK8s}
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
        {/if}
        <Field label={$_('step4.gateway')}>
          <input bind:value={$wizardStore.inventory.network.gateway} />
        </Field>
        <Field label={$_('step4.nameservers')}>
          <input value={nameserversText} oninput={(e) => nameserversInput((e.target as HTMLInputElement).value)} />
        </Field>
      </div>
    </Section>

    {#if showCeph}
      <Section title="Ceph 구성"
               subtitle="Ceph 데몬 네트워크와 풀 — 스토리지 노드들이 사용하는 네트워크는 K8s와 별개입니다.">
        <div class="grid-2">
          <Field label="Public network (CIDR)"
                 hint="mon/mgr/mds/osd가 클라이언트와 통신하는 네트워크. 모든 Ceph 노드 IP가 이 CIDR에 들어가야 함.">
            <input value={$wizardStore.inventory.ceph.public_network}
                   oninput={(e) => updateCeph({ public_network: (e.target as HTMLInputElement).value })}
                   placeholder="10.10.1.0/24" />
          </Field>
          <Field label="Cluster network (CIDR, 선택)"
                 hint="OSD 간 백엔드 복제 트래픽 분리용. 비워두면 public network 재사용. 권장: 10G 이상 별도 NIC.">
            <input value={$wizardStore.inventory.ceph.cluster_network}
                   oninput={(e) => updateCeph({ cluster_network: (e.target as HTMLInputElement).value })}
                   placeholder="172.16.1.0/24" />
          </Field>
        </div>

        <Field label="활성화할 풀"
               hint="rbd: K8s PVC 블록 / cephfs: ReadWriteMany 파일 / rgw: S3 호환 오브젝트">
          <div class="pool-row">
            <label class="pool-chip" class:active={$wizardStore.inventory.ceph.pools.includes('rbd')}>
              <input type="checkbox"
                     checked={$wizardStore.inventory.ceph.pools.includes('rbd')}
                     onchange={(e) => togglePool('rbd', (e.target as HTMLInputElement).checked)} />
              RBD <span class="pool-sub">(블록)</span>
            </label>
            <label class="pool-chip" class:active={$wizardStore.inventory.ceph.pools.includes('cephfs')}>
              <input type="checkbox"
                     checked={$wizardStore.inventory.ceph.pools.includes('cephfs')}
                     onchange={(e) => togglePool('cephfs', (e.target as HTMLInputElement).checked)} />
              CephFS <span class="pool-sub">(파일·RWX)</span>
            </label>
            <label class="pool-chip" class:active={$wizardStore.inventory.ceph.pools.includes('rgw')}>
              <input type="checkbox"
                     checked={$wizardStore.inventory.ceph.pools.includes('rgw')}
                     onchange={(e) => togglePool('rgw', (e.target as HTMLInputElement).checked)} />
              RGW <span class="pool-sub">(S3)</span>
            </label>
          </div>
        </Field>

        <div class="osd-defaults-head">OSD 기본값 (모든 OSD 노드에 적용, 노드별 override 가능)</div>
        <div class="grid-3">
          <Field label="복제 수 (replica)"
                 hint="RBD/CephFS 풀의 기본 replica. 3 = 표준 (host 2개 손실 OK), 2 = 랩 전용, 1 = 단일 노드만.">
            <select value={String($wizardStore.inventory.ceph.replication ?? 3)}
                    onchange={(e) => updateCeph({ replication: +(e.target as HTMLSelectElement).value })}>
              <option value="3">3 (표준 — 권장)</option>
              <option value="2">2 (랩 전용)</option>
              <option value="1">1 (단일 노드)</option>
              <option value="4">4 (고가용)</option>
              <option value="5">5 (최고가용)</option>
            </select>
          </Field>
          <Field label="Failure domain (CRUSH)"
                 hint="복제본을 분산할 단위. 'host': 노드 단위(표준), 'rack'/'chassis': CRUSH 토폴로지 사전 설정 필요, 'osd': 비권장.">
            <select value={$wizardStore.inventory.ceph.failure_domain ?? 'host'}
                    onchange={(e) => updateCeph({ failure_domain: (e.target as HTMLSelectElement).value as 'host' | 'rack' | 'chassis' | 'osd' })}>
              <option value="host">host (표준)</option>
              <option value="rack">rack</option>
              <option value="chassis">chassis</option>
              <option value="osd">osd (비권장)</option>
            </select>
          </Field>
          <Field label="기본 OSDs per device"
                 hint="노드별 osds_per_device가 비어있을 때 사용. 일반 클러스터: 1.">
            <input type="number" min="1" max="16"
                   value={$wizardStore.inventory.ceph.default_osds_per_device ?? 1}
                   oninput={(e) => updateCeph({ default_osds_per_device: +(e.target as HTMLInputElement).value || 1 })} />
          </Field>
        </div>
        <label class="checkbox">
          <input type="checkbox"
                 checked={$wizardStore.inventory.ceph.default_encrypted ?? false}
                 onchange={(e) => updateCeph({ default_encrypted: (e.target as HTMLInputElement).checked })} />
          <span>모든 OSD 기본 dm-crypt 암호화 (노드별 override 가능)</span>
        </label>

        {#if cephHints.length > 0 || datastoreHints.length > 0}
          <div class="ceph-hints">
            {#each cephHints as h}
              <div class="hint-row hint-{h.tone}">
                <Badge tone={h.tone}>{h.tone === 'danger' ? '필수' : '권장'}</Badge>
                <span>{h.msg}</span>
              </div>
            {/each}
            {#each datastoreHints as h}
              <div class="hint-row hint-{h.tone}">
                <Badge tone={h.tone}>분산</Badge>
                <span>{h.msg}</span>
              </div>
            {/each}
          </div>
        {/if}
      </Section>
    {/if}

    <Section title={$_('step4.nodes')}
             subtitle={$wizardStore.inventory.nodes.length + ' nodes'}>
      {#if $wizardStore.inventory.target.type === 'esxi'}
        {#if datastoreOptions.length > 0}
          <div class="discovery-banner ok">
            ✓ Step 2 discovery에서 <strong>{$wizardStore.discovered.datastores?.length ?? 0}</strong>개 datastore 발견
            (사용 가능 <strong>{datastoreOptions.length}</strong>개,
             제외 {($wizardStore.discovered.datastores?.length ?? 0) - datastoreOptions.length}개).
            노드별 "설치 디스크 위치" 드롭다운에서 선택하세요.
          </div>
        {:else}
          <div class="discovery-banner missing">
            ⚠ Step 2에서 ESXi discovery를 아직 실행하지 않았습니다.
            "연결 + 리소스 가져오기" 버튼을 눌러야 datastore 드롭다운이 채워집니다.
            지금 진행하면 노드별 datastore가 자유 텍스트 입력으로 표시됩니다.
          </div>
        {/if}
      {/if}

      <div class="presets-head">
        <span class="muted">프리셋 — 클릭하면 노드가 역할별 기본값으로 추가됩니다:</span>
        {#if $wizardStore.inventory.nodes.length > 0}
          <span class="bulk-divider">|</span>
          <button class="link-btn" onclick={expandAllNodes} type="button">전체 펼치기</button>
          <button class="link-btn" onclick={collapseAllNodes} type="button">전체 접기</button>
        {/if}
      </div>

      <div class="preset-grid">
        {#each visiblePresets as p}
          <div class="preset-card">
            <div class="preset-card-head">
              <span class="preset-icon" style="color: {p.color}">●</span>
              <strong>{p.label}</strong>
            </div>
            <div class="preset-badge">{p.badge}</div>
            <div class="preset-desc">{p.description}</div>
            <div class="preset-spec">
              {p.template.os ?? '?'} · {p.template.cpu}c/{p.template.memory_gb}G/{p.template.disk_gb}G
              {#if p.template.data_devices}· data:{p.template.data_devices.join(',')}{/if}
              {#if p.template.db_devices}· db:{p.template.db_devices.join(',')}{/if}
            </div>
            <div class="preset-actions">
              <label class="count-label">
                Count
                <select bind:value={presetCount[p.kind]}>
                  {#each p.countOptions as n}
                    <option value={n}>{n}</option>
                  {/each}
                </select>
              </label>
              <Button variant="primary"
                      onclick={() => addPreset(p.kind, presetCount[p.kind])}>
                + 추가
              </Button>
            </div>
          </div>
        {/each}
      </div>

      {#each $wizardStore.inventory.nodes as node, i}
        <div class="node-row" class:collapsed={!nodeExpanded[i]}>
          <div class="node-head-bar"
               role="button"
               tabindex="0"
               onclick={(e) => { if ((e.target as HTMLElement).closest('input,button,select,label')) return; toggleNode(i); }}
               onkeydown={(e) => { if ((e.key === 'Enter' || e.key === ' ') && !(e.target as HTMLElement).closest('input,button,select,label')) { e.preventDefault(); toggleNode(i); } }}>
            <span class="caret">{nodeExpanded[i] ? '▼' : '▶'}</span>

            {#if nodeExpanded[i]}
              <input class="hostname"
                     value={node.hostname}
                     placeholder="hostname"
                     oninput={(e) => updateNode(i, { hostname: (e.target as HTMLInputElement).value })} />
            {:else}
              <span class="hostname-display">{node.hostname || '(unnamed)'}</span>
              <span class="row-ip">{node.ip || '— no ip —'}</span>
              <div class="row-roles">
                {#each node.roles as r}<span class="row-role">{r}</span>{/each}
                {#if node.roles.length === 0}<span class="row-role muted-role">no roles</span>{/if}
              </div>
              <span class="row-spec">
                {node.os} · {node.cpu ?? '?'}c/{node.memory_gb ?? '?'}G/{node.disk_gb ?? '?'}G
              </span>
              {#if node.datastore}<span class="row-ds">{node.datastore}</span>{/if}
              {#if rowIssues(node).length > 0}
                <span class="row-issues">⚠ {rowIssues(node).join(', ')}</span>
              {/if}
            {/if}

            <span class="row-actions">
              <Button variant="ghost" onclick={() => removeNode(i)}>✕</Button>
            </span>
          </div>

          {#if nodeExpanded[i]}
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
          <div class="grid-2">
            <Field label="설치 디스크 위치 ({datastoreOptions.length} 개 발견)"
                   hint={datastoreOptions.length > 0
                     ? `Step 2 discovery로 가져온 ${datastoreOptions.length}개 datastore에서 선택. 모두 표시되지 않는다면 Step 2로 돌아가 "연결 + 리소스 가져오기"를 다시 눌러주세요.`
                     : 'Step 2의 "연결 + 리소스 가져오기"를 누르면 드롭다운으로 채워집니다. 비워두면 클러스터 기본값 사용.'}>
              {#if datastoreOptions.length > 0}
                <select value={node.datastore ?? ''}
                        onchange={(e) => updateNode(i, { datastore: (e.target as HTMLSelectElement).value || undefined })}>
                  <option value="">— 기본값({$wizardStore.inventory.target.datastore || '미지정'}) —</option>
                  {#each datastoreOptions as ds}
                    <option value={ds.name}>{ds.name} ({ds.type ?? 'VMFS'}, {ds.free_gb ? ds.free_gb.toFixed(0) : '?'} / {ds.capacity_gb ? ds.capacity_gb.toFixed(0) : '?'} GB)</option>
                  {/each}
                </select>
              {:else}
                <input value={node.datastore ?? ''}
                       oninput={(e) => updateNode(i, { datastore: (e.target as HTMLInputElement).value || undefined })}
                       placeholder={'(blank → ' + ($wizardStore.inventory.target.datastore || 'cluster default') + ')'} />
              {/if}
            </Field>

            <Field label="디스크 프로비저닝"
                   hint="Thin: 사용시 할당, 저장공간 절약 (OS 디스크 권장). Thick: 사전 전체 할당. Thick eager-zeroed: 사전 할당 + 0 채움, ESXi 전용 — Ceph OSD 데이터 디스크 권장 (first-write 페널티 회피).">
              <select value={node.disk_provisioning ?? 'thin'}
                      onchange={(e) => updateNode(i, { disk_provisioning: (e.target as HTMLSelectElement).value as 'thin' | 'thick' | 'thick-eager' })}>
                <option value="thin">Thin (기본 — 저장공간 절약)</option>
                <option value="thick">Thick (사전 할당, lazy zero)</option>
                <option value="thick-eager">Thick eager-zeroed (사전 할당 + 0 채움, ESXi)</option>
              </select>
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

          {#if node.roles.includes('ceph-osd')}
            <div class="osd-section">
              <div class="osd-head">OSD 디스크 명세</div>

              <div class="grid-2">
                <Field label="Data devices (OSD 데이터)"
                       hint="cephadm이 BlueStore OSD data로 사용할 블록 디바이스. 일반적으로 HDD. 예: /dev/sdb, /dev/sdc"
                       error={dataDevicesOf(node).length === 0 ? '최소 1개 필요 — cephadm bootstrap이 OSD를 만들지 못합니다' : ''}
                       required>
                  <input value={devicesText(dataDevicesOf(node))}
                         oninput={(e) => setDataDevices(i, (e.target as HTMLInputElement).value)}
                         placeholder="/dev/sdb, /dev/sdc" />
                </Field>

                <Field label="DB/WAL devices (BlueStore 메타)"
                       hint={(node.device_class ?? 'auto') === 'hdd'
                         ? '⚡ HDD에 강력 권장 — SSD/NVMe 경로 입력 시 OSD throughput 4-8× 개선 (BlueStore rocksdb를 SSD로 분리). 비워두면 data device 자체에 함께 저장.'
                         : '선택 — SSD/NVMe 경로 입력 시 BlueStore DB를 분리. 비워두면 data device에 함께 저장.'}>
                  <input value={devicesText(node.db_devices)}
                         oninput={(e) => setDBDevices(i, (e.target as HTMLInputElement).value)}
                         placeholder="/dev/nvme0n1" />
                </Field>
              </div>

              <button class="advanced-toggle" onclick={() => toggleAdvanced(i)} type="button">
                {advancedOSD[i] ? '▼' : '▶'} 고급 OSD 옵션
              </button>

              {#if advancedOSD[i]}
                <div class="grid-3 osd-advanced">
                  <Field label="Device class (CRUSH)"
                         hint="auto: 디스크 종류 자동 감지. hdd/ssd/nvme: 명시 — Ceph 풀 규칙에서 tier 분리 시 사용.">
                    <select value={node.device_class ?? 'auto'}
                            onchange={(e) => updateNode(i, { device_class: (e.target as HTMLSelectElement).value as 'auto' | 'hdd' | 'ssd' | 'nvme' })}>
                      <option value="auto">auto (감지)</option>
                      <option value="hdd">hdd</option>
                      <option value="ssd">ssd</option>
                      <option value="nvme">nvme</option>
                    </select>
                  </Field>

                  <Field label="OSDs per device"
                         hint="data device 1개당 OSD 데몬 수. HDD/SATA SSD는 1, 고IOPS NVMe는 2-4 (병렬 큐).">
                    <input type="number" min="1" max="16"
                           value={node.osds_per_device ?? 1}
                           oninput={(e) => updateNode(i, { osds_per_device: +(e.target as HTMLInputElement).value || 1 })} />
                  </Field>

                  <Field label="암호화 (dm-crypt)"
                         hint="OSD 데이터를 dm-crypt로 at-rest 암호화. 약간의 CPU 오버헤드 (AES-NI 있으면 미미).">
                    <label class="enc-row">
                      <input type="checkbox"
                             checked={node.osd_encrypted ?? ($wizardStore.inventory.ceph.default_encrypted ?? false)}
                             onchange={(e) => updateNode(i, { osd_encrypted: (e.target as HTMLInputElement).checked })} />
                      <span>{node.osd_encrypted ? 'enabled' : 'disabled'}</span>
                    </label>
                  </Field>

                  <Field label="WAL devices (별도, 드물게 사용)"
                         hint="DB와 분리된 WAL 위치. 거의 안 씀 — 보통 DB devices에 자동 포함. 명시하지 않으면 빈 채로 두세요.">
                    <input value={devicesText(node.wal_devices)}
                           oninput={(e) => updateNode(i, { wal_devices: parseDevices((e.target as HTMLInputElement).value).length > 0 ? parseDevices((e.target as HTMLInputElement).value) : undefined })}
                           placeholder="(empty)" />
                  </Field>
                </div>
              {/if}
            </div>
          {/if}
          {/if}
        </div>
      {/each}

      <div class="add-row">
        <Button variant="primary" onclick={addNodeManual}>+ {$_('step4.addNode')}</Button>
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

  .node-row { background: #0f0f12; border: 1px solid #2a2a30;
              border-radius: 6px; margin-bottom: 0.4rem;
              transition: border-color 0.1s; }
  .node-row:not(.collapsed) { border-color: #3b82f6; padding-bottom: 0.75rem; }

  .node-head-bar { display: flex; gap: 0.6rem; align-items: center;
                   padding: 0.55rem 0.75rem;
                   cursor: pointer; user-select: none;
                   background: transparent; border: none; outline: none;
                   text-align: left; font-family: inherit; color: inherit;
                   width: 100%; box-sizing: border-box; }
  .node-head-bar:hover { background: #1a1a1f; }
  .node-row:not(.collapsed) .node-head-bar { border-bottom: 1px solid #1e3a8a;
                                              margin-bottom: 0.5rem; }
  .caret { color: #71717a; font-size: 0.75rem; flex-shrink: 0;
           width: 0.9rem; text-align: center; }
  .node-row:not(.collapsed) .caret { color: #60a5fa; }

  .hostname-display { font-size: 0.92rem; font-weight: 500; color: #e4e4e7;
                      flex-shrink: 0; min-width: 9rem; }
  .row-ip { color: #93c5fd; font-family: ui-monospace, monospace;
            font-size: 0.78rem; flex-shrink: 0; min-width: 8rem; }
  .row-roles { display: flex; gap: 0.25rem; flex-wrap: wrap; flex-shrink: 0; }
  .row-role { display: inline-block; padding: 0.05rem 0.4rem; border-radius: 999px;
              background: #1e293b; border: 1px solid #3b82f6; color: #93c5fd;
              font-size: 0.68rem; font-family: ui-monospace, monospace;
              line-height: 1.3; }
  .row-role.muted-role { background: #27272a; border-color: #52525b; color: #71717a; }
  .row-spec { color: #a1a1aa; font-size: 0.75rem; font-family: ui-monospace, monospace;
              flex-shrink: 0; }
  .row-ds { color: #fbbf24; font-size: 0.75rem; font-family: ui-monospace, monospace;
            background: #1a1410; padding: 0.05rem 0.4rem; border-radius: 3px;
            flex-shrink: 0; }
  .row-issues { color: #fca5a5; font-size: 0.75rem; flex-shrink: 0;
                background: #7f1d1d40; padding: 0.05rem 0.4rem; border-radius: 3px; }
  .row-actions { margin-left: auto; flex-shrink: 0; }

  .hostname { flex: 1; background: #1b1b1f; color: #e4e4e7; border: 1px solid #3f3f46;
              border-radius: 5px; padding: 0.4rem 0.6rem; font-size: 0.9rem; font-weight: 500;
              font-family: inherit; outline: none; }
  .hostname:focus { border-color: #60a5fa; }

  .bulk-divider { color: #3f3f46; margin: 0 0.25rem; }
  .link-btn { background: none; border: none; color: #93c5fd; cursor: pointer;
              font-size: 0.78rem; padding: 0.3rem 0.5rem; font-family: inherit;
              border-radius: 3px; }
  .link-btn:hover { background: #1e293b; }

  .node-row:not(.collapsed) .grid-3,
  .node-row:not(.collapsed) .grid-2 { padding: 0 0.75rem; }
  .node-row:not(.collapsed) > :global(label),
  .node-row:not(.collapsed) > .osd-section { margin: 0 0.75rem; }

  .roles { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .role-chip { display: flex; gap: 0.3rem; align-items: center; padding: 0.2rem 0.55rem;
               background: #27272a; border: 1px solid #3f3f46; border-radius: 999px;
               font-size: 0.75rem; cursor: pointer; user-select: none; color: #a1a1aa; }
  .role-chip.active { background: #1e293b; border-color: #3b82f6; color: #93c5fd; }
  .role-chip input { display: none; }

  .add-row { margin-top: 0.5rem; }
  .presets-head { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 0.6rem; }
  .preset-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
                 gap: 0.6rem; margin-bottom: 1rem; }
  .preset-card { background: #0f0f12; border: 1px solid #2a2a30; border-radius: 6px;
                 padding: 0.7rem 0.85rem; display: flex; flex-direction: column;
                 gap: 0.3rem; }
  .preset-card-head { display: flex; align-items: center; gap: 0.4rem; }
  .preset-card-head strong { font-size: 0.88rem; color: #e4e4e7; }
  .preset-icon { font-size: 0.85rem; line-height: 1; }
  .preset-badge { font-size: 0.7rem; color: #93c5fd; font-family: ui-monospace, monospace;
                  background: #1e293b; padding: 0.05rem 0.4rem; border-radius: 3px;
                  align-self: flex-start; }
  .preset-desc { font-size: 0.74rem; color: #a1a1aa; line-height: 1.4;
                 min-height: 2.6em; }
  .preset-spec { font-size: 0.7rem; color: #71717a; font-family: ui-monospace, monospace; }
  .preset-actions { display: flex; gap: 0.4rem; align-items: center;
                    margin-top: 0.4rem; padding-top: 0.4rem;
                    border-top: 1px solid #1e1e22; }
  .count-label { display: flex; gap: 0.35rem; align-items: center;
                 font-size: 0.78rem; color: #a1a1aa; flex: 1; }
  .count-label select { width: 4.5rem; padding: 0.3rem 0.4rem; background: #1b1b1f;
                        color: #e4e4e7; border: 1px solid #3f3f46; border-radius: 4px;
                        font-family: inherit; font-size: 0.85rem; outline: none; }
  .count-label select:focus { border-color: #60a5fa; }
  .muted { color: #71717a; font-size: 0.8rem; }
  .row { display: flex; gap: 0.75rem; align-items: center; margin-top: 0.5rem; }

  .errors { background: #1f1f23; border: 1px solid #7f1d1d; color: #fca5a5;
            padding: 0.6rem; border-radius: 5px; font-size: 0.8rem;
            font-family: ui-monospace, monospace; white-space: pre-wrap; }

  .yaml { background: #0a0a0c; border: 1px solid #2a2a30; padding: 0.75rem;
          border-radius: 5px; font-family: ui-monospace, monospace; font-size: 0.78rem;
          line-height: 1.5; max-height: 70vh; overflow: auto; color: #d4d4d8; margin: 0; }

  .pool-row { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .pool-chip { display: flex; gap: 0.4rem; align-items: center; padding: 0.35rem 0.7rem;
               background: #27272a; border: 1px solid #3f3f46; border-radius: 5px;
               font-size: 0.85rem; cursor: pointer; user-select: none; color: #a1a1aa; }
  .pool-chip.active { background: #1e293b; border-color: #f59e0b; color: #fbbf24; }
  .pool-chip input { accent-color: #f59e0b; }
  .pool-sub { font-size: 0.7rem; color: #71717a; }
  .pool-chip.active .pool-sub { color: #d97706; }

  .ceph-hints { display: flex; flex-direction: column; gap: 0.35rem; margin-top: 0.5rem;
                padding-top: 0.75rem; border-top: 1px solid #2a2a30; }
  .hint-row { display: flex; gap: 0.5rem; align-items: flex-start; font-size: 0.8rem;
              line-height: 1.5; color: #d4d4d8; }
  .hint-row span { flex: 1; }

  .discovery-banner { padding: 0.6rem 0.8rem; border-radius: 5px; font-size: 0.82rem;
                      line-height: 1.5; margin-bottom: 0.75rem; }
  .discovery-banner strong { color: inherit; font-weight: 600; }
  .discovery-banner.ok      { background: #14532d20; border: 1px solid #16a34a;
                              color: #86efac; }
  .discovery-banner.missing { background: #78350f20; border: 1px solid #d97706;
                              color: #fde68a; }

  .osd-section { margin-top: 0.6rem; padding: 0.7rem 0.8rem;
                 background: #1a1410; border: 1px solid #422006;
                 border-radius: 5px; }
  .osd-head { font-size: 0.75rem; color: #f59e0b; font-weight: 600;
              text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; }
  .advanced-toggle { background: none; border: none; color: #93c5fd; cursor: pointer;
                     font-size: 0.78rem; padding: 0.4rem 0; margin-top: 0.5rem;
                     font-family: inherit; }
  .advanced-toggle:hover { text-decoration: underline; }
  .osd-advanced { margin-top: 0.5rem; padding-top: 0.5rem;
                  border-top: 1px dashed #44403c; }
  .enc-row { display: flex; gap: 0.5rem; align-items: center;
             padding: 0.45rem 0.6rem; background: #0f0f12;
             border: 1px solid #3f3f46; border-radius: 5px;
             font-size: 0.85rem; cursor: pointer; }
  .enc-row input { accent-color: #f59e0b; }

  .osd-defaults-head { font-size: 0.75rem; color: #f59e0b; font-weight: 600;
                       text-transform: uppercase; letter-spacing: 0.05em;
                       margin: 0.75rem 0 0.4rem; padding-top: 0.6rem;
                       border-top: 1px solid #2a2a30; }
  .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.6rem; }
</style>
