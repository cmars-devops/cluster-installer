<script lang="ts">
  import { _ } from 'svelte-i18n';
  import Section from '../lib/ui/Section.svelte';
  import Field from '../lib/ui/Field.svelte';
  import Button from '../lib/ui/Button.svelte';
  import StepNav from '../lib/ui/StepNav.svelte';
  import Badge from '../lib/ui/Badge.svelte';
  import {
    wizardStore, addNode, removeNode, updateNode,
    addDevVMDisk, removeDevVMDisk, updateDevVMDisk,
    addDevVMNIC, removeDevVMNIC, updateDevVMNIC, syncPrimaryFromLegacy,
    type Role, type NodeSpec, type DiskSpec, type NICSpec
  } from '../stores/wizard';
  import { api } from '../lib/api';

  const k8sRoles: Role[]  = ['control-plane', 'etcd', 'worker'];
  const cephRoles: Role[] = ['ceph-mon', 'ceph-mgr', 'ceph-osd', 'ceph-mds', 'ceph-rgw'];

  // Ceph cluster_network는 OSD 데몬 간 트래픽 전용. mon/mgr/mds/rgw는 바인드하지 않음.
  function isCephCoreOnly(roles: Role[]): boolean {
    const hasAnyCeph = roles.some((r) => r.startsWith('ceph-'));
    const hasOSD = roles.includes('ceph-osd');
    return hasAnyCeph && !hasOSD;
  }

  // OS 라벨은 Step 3에서 결정. Step 4에서는 읽기 전용 표시만.
  function osLabel(os: NodeSpec['os']): string {
    switch (os) {
      case 'microos':    return 'openSUSE MicroOS';
      case 'leap':       return 'openSUSE Leap 16';
      case 'tumbleweed': return 'openSUSE Tumbleweed';
      case 'ubuntu':     return 'Ubuntu 26.04 LTS';
      default:           return os;
    }
  }

  const topology = $derived($wizardStore.inventory.cluster.topology);
  const devVMMode = $derived(topology === 'dev-vm');
  const showK8s   = $derived(topology === 'k8s-only'  || topology === 'combined');
  const showCeph  = $derived(topology === 'ceph-only' || topology === 'combined');

  // ── dev-vm: single VM, no roles, no cluster networking ─────────────
  // The whole "독립적인 VM" UX lives here. nodes[0] is the only node.
  const devVMNode = $derived<NodeSpec | undefined>(
    devVMMode ? $wizardStore.inventory.nodes[0] : undefined
  );
  function updateDevVMNode(patch: Partial<NodeSpec>) {
    updateNode(0, patch);
    // When the operator added extras, disks[0]/nics[0] must mirror the
    // legacy primary fields. The store helper is a no-op when neither
    // array is populated, so cluster-mode pathways are untouched.
    syncPrimaryFromLegacy($wizardStore.inventory.target.network || 'VM Network');
  }
  function updateNetwork(patch: Partial<typeof $wizardStore.inventory.network>) {
    wizardStore.update((s) => ({
      ...s,
      inventory: { ...s.inventory, network: { ...s.inventory.network, ...patch } }
    }));
  }
  // Same shape as Step 2's updateTarget — used by the dev-vm primary
  // NIC port-group picker that lives here so the operator can change
  // it without backtracking to Step 2.
  function updateTarget(patch: Partial<typeof $wizardStore.inventory.target>) {
    wizardStore.update((s) => ({
      ...s,
      inventory: { ...s.inventory, target: { ...s.inventory.target, ...patch } }
    }));
  }
  // IPv4 octet check reused for the per-NIC IP/gateway/nameservers below.
  function setNameserversText(value: string) {
    updateNetwork({
      nameservers: value.split(',').map((x) => x.trim()).filter(Boolean)
    });
  }

  // IPv4 dotted-quad sanity check: each octet 0-255, exactly four of them.
  // Used to gate "다음" and to render an inline warning so a typo like
  // 10.10.1.3 → 10.10.13 doesn't slip into netplan and fail the install.
  function isValidIPv4(s: string): boolean {
    const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s);
    if (!m) return false;
    for (let i = 1; i <= 4; i++) {
      const n = +m[i];
      if (!(n >= 0 && n <= 255)) return false;
    }
    return true;
  }
  const invalidNameservers = $derived(
    ($wizardStore.inventory.network.nameservers ?? []).filter((n) => !isValidIPv4(n))
  );
  const invalidGateway = $derived(
    !!$wizardStore.inventory.network.gateway && !isValidIPv4($wizardStore.inventory.network.gateway)
  );
  const invalidIP = $derived(
    !!devVMNode?.ip && !isValidIPv4(devVMNode.ip)
  );
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
    const node = $wizardStore.inventory.nodes[idx];
    const next = checked ? [...node.roles, role] : node.roles.filter((r) => r !== role);
    const patch: Partial<NodeSpec> = { roles: next };
    if (isCephCoreOnly(next) && node.cluster_ip) {
      patch.cluster_ip = undefined;
    }
    updateNode(idx, patch);
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
      defaultCount: 3, countOptions: [1, 2, 3, 4, 5, 6, 7, 8, 10, 12],
      template: {
        roles: ['ceph-osd'], os: 'leap',
        cpu: 4, memory_gb: 6, disk_gb: 64,
        data_devices: ['/dev/sdb'],
        db_devices:   ['/dev/sdc'],
        // Per-node disk sizes — heterogeneous OSD clusters can override
        // these freely (different OSD nodes can have different HDD/SSD
        // sizes without a cluster-wide default).
        osd_data_size_gb: 64,
        osd_db_size_gb: 16,
        device_class: 'hdd',
        osds_per_device: 1,
        osd_encrypted: false,
        // Thin: VM provisioning is hardware allocation only. Thick-eager
        // (zero-fill at create) is a deployment-time perf decision —
        // operators flip per-node before Apply if they want it.
        disk_provisioning: 'thin'
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

  // ── Sample inventory loaders ──────────────────────────────────────────
  // Single-click "load this entire pattern" — speed up common scenarios
  // and give new users a working baseline they can edit.
  function clearAllNodes() {
    wizardStore.update((s) => ({
      ...s, inventory: { ...s.inventory, nodes: [] }
    }));
    nodeExpanded = {};
  }
  function loadSample(kind: 'idc-full' | 'idc-ceph' | 'idc-k3s' | 'lab-min') {
    if (!confirm('현재 노드 목록을 비우고 샘플로 교체하시겠습니까?')) return;
    clearAllNodes();
    if (kind === 'idc-full' || kind === 'idc-ceph') {
      addPreset('ceph-core', 3);
      addPreset('ceph-osd',  3);
      addPreset('ceph-rgw',  2);
    }
    if (kind === 'idc-full' || kind === 'idc-k3s') {
      // K3s uses different IP base; addPreset handles it via ipBase=31.
      addPreset('rke2-cp',     3);
      addPreset('rke2-worker', 4);
    }
    if (kind === 'lab-min') {
      addPreset('k3s-single', 1);
    }
    if (kind === 'idc-full') {
      // Combined: also flip topology + auto-fill RGW realm/external Ceph if relevant.
      wizardStore.update((s) => ({
        ...s,
        inventory: {
          ...s.inventory,
          cluster: { ...s.inventory.cluster, topology: 'combined' }
        }
      }));
    } else if (kind === 'idc-ceph') {
      wizardStore.update((s) => ({
        ...s,
        inventory: {
          ...s.inventory,
          cluster: { ...s.inventory.cluster, topology: 'ceph-only' }
        }
      }));
    } else if (kind === 'idc-k3s' || kind === 'lab-min') {
      wizardStore.update((s) => ({
        ...s,
        inventory: {
          ...s.inventory,
          cluster: { ...s.inventory.cluster, topology: 'k8s-only' }
        }
      }));
    }
  }

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
    // Step 3에서 선택한 OS 선호도를 적용 — 프리셋 템플릿에 하드코딩된
    // os 필드(leap/microos)는 무시하고 ceph-* 프리셋이면 cephOS,
    // 그 외는 k8sOS로 덮어씀.
    const isCephPreset = p.template.roles?.some((r) => r.startsWith('ceph-')) ?? false;
    const preferredOS = isCephPreset
      ? $wizardStore.osPreferences.ceph
      : $wizardStore.osPreferences.k8s;
    for (let n = 0; n < count; n++) {
      const idx = startIdx + n;
      const idxStr = String(idx).padStart(2, '0');
      const lastOct = p.ipBase + idx - 1;
      const node: Partial<NodeSpec> = {
        ...p.template,
        os: preferredOS,
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

  function updateK8s(patch: Partial<typeof $wizardStore.inventory.cluster.kubernetes>) {
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        cluster: {
          ...s.inventory.cluster,
          kubernetes: { ...s.inventory.cluster.kubernetes, ...patch }
        }
      }
    }));
  }

  function generateToken(): string {
    // 32 bytes hex == 64 chars. RKE2/K3s accept any opaque token; this
    // matches what their bootstrap scripts auto-generate.
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
  }

  // ── External Ceph connection (k8s-only + existing Ceph cluster) ───────
  function updateExternalCeph(patch: Partial<NonNullable<typeof $wizardStore.inventory.cluster.external_ceph>>) {
    wizardStore.update((s) => {
      const cur = s.inventory.cluster.external_ceph ?? {
        mon_endpoints: [], fsid: '', client_user: 'k8s-rbd', client_key: '', pool: 'rbd-pool'
      };
      return {
        ...s,
        inventory: {
          ...s.inventory,
          cluster: {
            ...s.inventory.cluster,
            external_ceph: { ...cur, ...patch }
          }
        }
      };
    });
  }
  function toggleExternalCeph(on: boolean) {
    wizardStore.update((s) => ({
      ...s,
      inventory: {
        ...s.inventory,
        cluster: {
          ...s.inventory.cluster,
          external_ceph: on
            ? (s.inventory.cluster.external_ceph ?? {
                mon_endpoints: [], fsid: '', client_user: 'k8s-rbd', client_key: '', pool: 'rbd-pool'
              })
            : undefined
        }
      }
    }));
  }
  const useExternalCeph = $derived($wizardStore.inventory.cluster.external_ceph !== undefined);
  const externalCeph = $derived($wizardStore.inventory.cluster.external_ceph);

  function monsText(): string {
    return (externalCeph?.mon_endpoints ?? []).join(', ');
  }
  function setMons(text: string) {
    updateExternalCeph({ mon_endpoints: text.split(',').map(s => s.trim()).filter(Boolean) });
  }
  function tlsSansText(): string {
    return ($wizardStore.inventory.cluster.kubernetes.tls_sans ?? []).join(', ');
  }
  function setTLSSans(text: string) {
    updateK8s({ tls_sans: text.split(',').map(s => s.trim()).filter(Boolean) });
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
  // Concise per-node validation summary surfaced on the collapsed row.
  function rowIssues(n: NodeSpec): string[] {
    const errs: string[] = [];
    if (!n.ip) errs.push('IP 누락');
    if (n.roles.length === 0) errs.push('역할 없음');
    if (n.roles.includes('ceph-osd') && dataDevicesOf(n).length === 0) errs.push('Data devices 누락');
    return errs;
  }

  // ── Cross-node / cross-field validation ───────────────────────────────
  // Catches issues that aren't visible from any single node row.
  function ipInRange(ip: string, range: string): boolean {
    // Quick "10.10.1.41-10.10.1.49" range check. IPs are .NN format only.
    const m = range.match(/^(\d+\.\d+\.\d+)\.(\d+)-\1\.(\d+)$/);
    if (!m) return false;
    const ipM = ip.match(new RegExp(`^${m[1]}\\.(\\d+)$`));
    if (!ipM) return false;
    const n = +ipM[1], lo = +m[2], hi = +m[3];
    return n >= lo && n <= hi;
  }

  const clusterIssues = $derived.by(() => {
    const out: { tone: 'danger' | 'warn' | 'info'; msg: string }[] = [];
    const nodes = $wizardStore.inventory.nodes;
    const net = $wizardStore.inventory.network;

    // Hostname uniqueness
    const hostCount = new Map<string, number>();
    for (const n of nodes) {
      if (!n.hostname) continue;
      hostCount.set(n.hostname, (hostCount.get(n.hostname) ?? 0) + 1);
    }
    const dupeHosts = [...hostCount.entries()].filter(([, c]) => c > 1).map(([h]) => h);
    if (dupeHosts.length > 0) {
      out.push({ tone: 'danger',
        msg: `중복 hostname: ${dupeHosts.join(', ')} — 각 노드 hostname은 고유해야 합니다.` });
    }

    // Primary IP uniqueness across nodes
    const ipCount = new Map<string, number>();
    for (const n of nodes) {
      if (!n.ip) continue;
      ipCount.set(n.ip, (ipCount.get(n.ip) ?? 0) + 1);
    }
    const dupeIPs = [...ipCount.entries()].filter(([, c]) => c > 1).map(([h]) => h);
    if (dupeIPs.length > 0) {
      out.push({ tone: 'danger',
        msg: `중복 IP: ${dupeIPs.join(', ')} — 같은 IP를 여러 노드에 할당할 수 없습니다.` });
    }

    // Cluster-network IP uniqueness (Ceph C-Net)
    const cipCount = new Map<string, number>();
    for (const n of nodes) {
      if (!n.cluster_ip) continue;
      cipCount.set(n.cluster_ip, (cipCount.get(n.cluster_ip) ?? 0) + 1);
    }
    const dupeCIPs = [...cipCount.entries()].filter(([, c]) => c > 1).map(([h]) => h);
    if (dupeCIPs.length > 0) {
      out.push({ tone: 'danger',
        msg: `중복 cluster IP: ${dupeCIPs.join(', ')}` });
    }

    // VIP must not equal any node IP
    if (net.vip && nodes.some(n => n.ip === net.vip)) {
      out.push({ tone: 'danger',
        msg: `Control-plane VIP (${net.vip})가 노드 IP와 겹칩니다 — kube-vip가 VIP를 띄울 때 충돌.` });
    }

    // VIP must not fall inside lb_pool
    if (net.vip && net.lb_pool && ipInRange(net.vip, net.lb_pool)) {
      out.push({ tone: 'danger',
        msg: `Control-plane VIP (${net.vip})가 LoadBalancer 풀(${net.lb_pool}) 안에 있음 — MetalLB가 같은 IP를 서비스에 할당할 수 있음.` });
    }

    // Ingress LB IP must not equal any node IP
    if (net.ingress_lb_ip && nodes.some(n => n.ip === net.ingress_lb_ip)) {
      out.push({ tone: 'danger',
        msg: `Ingress LB IP (${net.ingress_lb_ip})가 노드 IP와 겹칩니다.` });
    }

    // lb_pool range overlap with node IPs
    if (net.lb_pool) {
      const conflictingNodes = nodes.filter(n => n.ip && ipInRange(n.ip, net.lb_pool));
      if (conflictingNodes.length > 0) {
        out.push({ tone: 'warn',
          msg: `LoadBalancer 풀(${net.lb_pool})에 노드 IP가 포함됨: ${conflictingNodes.map(n => n.hostname).join(', ')} — MetalLB가 노드 IP와 충돌하는 서비스 IP를 만들 수 있음.` });
      }
    }

    return out;
  });

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
nodes:${inv.nodes.length === 0 ? ' []' : '\n' + inv.nodes.map((n) => `  - hostname: ${n.hostname}
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
  <p>{devVMMode ? $_('step4.devVMSubtitle') : $_('step.4.subtitle')}</p>
</header>

{#if devVMMode && devVMNode}
  {@const ipMode = devVMNode.ip_mode ?? 'static'}
  {@const isStatic = ipMode === 'static'}
  {@const dsAvailable = ($wizardStore.discovered.datastores ?? []).filter((d) => d.accessible !== false)}
  {@const staticOK = ipMode !== 'static' || (!!devVMNode.ip && !invalidIP && !!$wizardStore.inventory.network.gateway && !invalidGateway && invalidNameservers.length === 0)}
  {@const extraNICsOK = (devVMNode.nics ?? []).slice(1).every((nic) => {
    if (!nic.network) return false;
    if ((nic.ip_mode ?? 'dhcp') !== 'static') return true;
    return !!nic.ip && isValidIPv4(nic.ip) && (!nic.gateway || isValidIPv4(nic.gateway));
  })}
  {@const extraDisksOK = (devVMNode.disks ?? []).slice(1).every((d) => (d.size_gb ?? 0) > 0)}
  {@const canAdvance = !!devVMNode.hostname && (ipMode === 'dhcp' || (!!devVMNode.ip && staticOK)) && !!(devVMNode.datastore || $wizardStore.inventory.target.datastore) && extraNICsOK && extraDisksOK}

  <!-- ── 1) VM 명세 ─────────────────────────────────────────────────
       Identity-only block: hostname (OS-internal) + vSphere display
       label. CPU / memory / disk / NIC each get their own section
       below so the form reads top-down as: who → resources → disks →
       network. -->
  <Section title={$_('step4.devVMSection')} subtitle={$_('step4.devVMSectionHint')}>
    <div class="grid-2">
      <Field label={$_('step4.devVM.hostname')} hint={$_('step4.devVM.hostnameHint')} required>
        <input value={devVMNode.hostname}
               oninput={(e) => updateDevVMNode({ hostname: (e.target as HTMLInputElement).value })}
               placeholder="devvm-01" />
      </Field>
      <Field label={$_('step4.devVM.displayName')} hint={$_('step4.devVM.displayNameHint')}>
        <input value={devVMNode.display_name ?? ''}
               oninput={(e) => updateDevVMNode({ display_name: (e.target as HTMLInputElement).value })}
               placeholder={devVMNode.hostname || '(호스트네임과 동일)'} />
      </Field>
    </div>
  </Section>

  <!-- ── 2) 자원 (CPU & Memory) ────────────────────────────────────
       Disk(GB) used to live here too, but that mixed VM-level sizing
       with per-disk identity. The OS disk is now a row in the disk
       section (same shape as extras), keeping every disk in one place. -->
  <Section title={$_('step4.devVM.resourcesTitle')} subtitle={$_('step4.devVM.resourcesHint')}>
    <div class="grid-2">
      <Field label={$_('step4.node.cpu')}>
        <input type="number" min="1"
               value={devVMNode.cpu}
               oninput={(e) => updateDevVMNode({ cpu: +(e.target as HTMLInputElement).value || 2 })} />
      </Field>
      <Field label={$_('step4.node.ram')}>
        <input type="number" min="1"
               value={devVMNode.memory_gb}
               oninput={(e) => updateDevVMNode({ memory_gb: +(e.target as HTMLInputElement).value || 4 })} />
      </Field>
    </div>
    <details class="cephadm-advanced">
      <summary>
        <span class="adv-title">{$_('step4.devVM.esxiAdvanced')}</span>
        <span class="adv-sub">{$_('step4.devVM.esxiAdvancedHint')}</span>
      </summary>
      <div class="adv-body">
        <p class="muted">{$_('step4.devVM.esxiAdvancedTodo')}</p>
      </div>
    </details>
  </Section>

  <!-- ── 3) 디스크 — 기본 OS 디스크 + 추가 디스크 N개 ──────────────
       Primary OS disk uses the SAME row layout as extras (size +
       datastore + provisioning + label) — only the badge differs (OS
       vs #2/#3) and the OS row has no remove-× button. Bindings stay
       on the legacy NodeSpec fields (disk_gb / datastore /
       disk_provisioning) so EffectiveDisks() keeps the cluster path
       intact. -->
  <Section title={$_('step4.devVM.disksTitle')} subtitle={$_('step4.devVM.disksHint')}>
    <!-- Primary OS disk row -->
    <div class="extra-row">
      <div class="extra-row-head">
        <span class="row-badge">{$_('step4.devVM.diskOS')}</span>
      </div>
      <div class="grid-4">
        <Field label={$_('step4.devVM.diskSize')} required>
          <input type="number" min="10"
                 value={devVMNode.disk_gb}
                 oninput={(e) => updateDevVMNode({ disk_gb: +(e.target as HTMLInputElement).value || 40 })} />
        </Field>
        <Field label={$_('step4.devVM.datastore')} hint={$_('step4.devVM.datastoreHint')} required>
          {#if dsAvailable.length > 0}
            <select value={devVMNode.datastore ?? ''}
                    onchange={(e) => updateDevVMNode({ datastore: (e.target as HTMLSelectElement).value })}>
              <option value="">— {$_('step4.devVM.datastorePicker')} —</option>
              {#each dsAvailable as ds}
                <option value={ds.name}>
                  {ds.name}{ds.type ? ` (${ds.type}` : ''}{ds.free_gb ? `, ${ds.free_gb.toFixed(0)} / ${ds.capacity_gb?.toFixed(0)} GB` : ''}{ds.type ? ')' : ''}
                </option>
              {/each}
            </select>
          {:else}
            <input value={devVMNode.datastore ?? ''}
                   oninput={(e) => updateDevVMNode({ datastore: (e.target as HTMLInputElement).value })}
                   placeholder="datastore1" />
          {/if}
        </Field>
        <Field label={$_('step4.devVM.diskProvisioning')} hint={$_('step4.devVM.diskProvisioningHint')}>
          <select value={devVMNode.disk_provisioning ?? 'thin'}
                  onchange={(e) => updateDevVMNode({ disk_provisioning: (e.target as HTMLSelectElement).value as 'thin' | 'thick' | 'thick-eager' })}>
            <option value="thin">thin (희소)</option>
            <option value="thick">thick (즉시, lazy zero)</option>
            <option value="thick-eager">thick-eager (즉시 + 0 채움)</option>
          </select>
        </Field>
        <Field label={$_('step4.devVM.diskLabel')}>
          <input value="OS" disabled class="readonly-input" />
        </Field>
      </div>
    </div>

    <!-- Extra disk rows -->
    {#each (devVMNode.disks ?? []).slice(1) as disk, idx}
      {@const realIdx = idx + 1}
      <div class="extra-row">
        <div class="extra-row-head">
          <span class="row-badge muted">{$_('step4.devVM.extraDisk')} #{realIdx}</span>
          <button type="button" class="row-remove" onclick={() => removeDevVMDisk(realIdx)}
                  title={$_('step4.devVM.removeDisk')}>×</button>
        </div>
        <div class="grid-4">
          <Field label={$_('step4.devVM.diskSize')}>
            <input type="number" min="1"
                   value={disk.size_gb}
                   oninput={(e) => updateDevVMDisk(realIdx, { size_gb: +(e.target as HTMLInputElement).value || 100 })} />
          </Field>
          <Field label={$_('step4.devVM.datastore')}>
            {#if dsAvailable.length > 0}
              <select value={disk.datastore ?? ''}
                      onchange={(e) => updateDevVMDisk(realIdx, { datastore: (e.target as HTMLSelectElement).value })}>
                <option value="">— ({$_('step4.devVM.usePrimary')}) —</option>
                {#each dsAvailable as ds}
                  <option value={ds.name}>{ds.name}</option>
                {/each}
              </select>
            {:else}
              <input value={disk.datastore ?? ''}
                     oninput={(e) => updateDevVMDisk(realIdx, { datastore: (e.target as HTMLInputElement).value })} />
            {/if}
          </Field>
          <Field label={$_('step4.devVM.diskProvisioning')}>
            <select value={disk.provisioning ?? 'thin'}
                    onchange={(e) => updateDevVMDisk(realIdx, { provisioning: (e.target as HTMLSelectElement).value as 'thin' | 'thick' | 'thick-eager' })}>
              <option value="thin">thin</option>
              <option value="thick">thick</option>
              <option value="thick-eager">thick-eager</option>
            </select>
          </Field>
          <Field label={$_('step4.devVM.diskLabel')}>
            <input value={disk.label ?? ''}
                   placeholder="data, logs, …"
                   oninput={(e) => updateDevVMDisk(realIdx, { label: (e.target as HTMLInputElement).value })} />
          </Field>
        </div>
      </div>
    {/each}

    <div class="add-row">
      <Button variant="ghost" onclick={() => addDevVMDisk()}>
        + {$_('step4.devVM.addDisk')}
      </Button>
    </div>
  </Section>

  <!-- ── 4) NIC — 기본 NIC + 추가 NIC N개 ──────────────────────────
       Same row pattern: primary uses the legacy fields (target.network
       + devVMNode.ip_mode + devVMNode.ip + network.gateway/prefix/dns)
       so EffectiveNICs() can synthesise correctly when no extras
       exist. Extras live in devVMNode.nics[1..]. -->
  <Section title={$_('step4.devVM.nicsTitleAll')} subtitle={$_('step4.devVM.nicsHintAll')}>
    <!-- Primary NIC row -->
    <div class="extra-row">
      <div class="extra-row-head">
        <span class="row-badge">{$_('step4.devVM.nicPrimary')}</span>
      </div>
      <div class="grid-2">
        <Field label={$_('step4.devVM.nicNetwork')} hint={$_('step4.devVM.nicNetworkHint')} required>
          {#if ($wizardStore.discovered.networks ?? []).length > 0}
            <select value={$wizardStore.inventory.target.network ?? ''}
                    onchange={(e) => updateTarget({ network: (e.target as HTMLSelectElement).value })}>
              <option value="">— {$_('step4.devVM.networkPicker')} —</option>
              {#each $wizardStore.discovered.networks ?? [] as net}
                <option value={net.name}>
                  {net.name}{net.vswitch ? `  (${net.vswitch}` : ''}{net.vlan_id ? `, VLAN ${net.vlan_id}` : ''}{net.vswitch ? ')' : ''}
                </option>
              {/each}
            </select>
          {:else}
            <input value={$wizardStore.inventory.target.network ?? ''}
                   oninput={(e) => updateTarget({ network: (e.target as HTMLInputElement).value })}
                   placeholder="VM Network" />
          {/if}
        </Field>
        <Field label={$_('step4.devVM.nicLabel')}>
          <input value="primary" disabled class="readonly-input" />
        </Field>
      </div>

      <div class="ipmode-row">
        <span class="ipmode-label">{$_('step4.devVM.ipMode')}</span>
        <label class="ipmode-radio">
          <input type="radio" name="ipmode" checked={ipMode === 'dhcp'}
                 onchange={() => updateDevVMNode({ ip_mode: 'dhcp', ip: '' })} />
          <span>DHCP</span>
          <small>{$_('step4.devVM.ipModeDHCPHint')}</small>
        </label>
        <label class="ipmode-radio">
          <input type="radio" name="ipmode" checked={ipMode === 'static'}
                 onchange={() => updateDevVMNode({ ip_mode: 'static' })} />
          <span>{$_('step4.devVM.ipModeStatic')}</span>
          <small>{$_('step4.devVM.ipModeStaticHint')}</small>
        </label>
      </div>

      {#if isStatic}
        <div class="grid-4">
          <Field label={$_('step4.node.ip')} hint="10.10.1.50" required>
            <input value={devVMNode.ip}
                   class:input-error={invalidIP}
                   oninput={(e) => updateDevVMNode({ ip: (e.target as HTMLInputElement).value })} />
            {#if invalidIP}
              <span class="input-warn">⚠ {$_('step4.devVM.invalidIPv4')}</span>
            {/if}
          </Field>
          <Field label={$_('step4.devVM.prefixLen')} hint="/24 → 24">
            <input type="number" min="8" max="30"
                   value={$wizardStore.inventory.network.prefix_len ?? 24}
                   oninput={(e) => updateNetwork({ prefix_len: +(e.target as HTMLInputElement).value || 24 })} />
          </Field>
          <Field label={$_('step4.gateway')} hint="10.10.1.1" required>
            <input value={$wizardStore.inventory.network.gateway}
                   class:input-error={invalidGateway}
                   oninput={(e) => updateNetwork({ gateway: (e.target as HTMLInputElement).value })} />
            {#if invalidGateway}
              <span class="input-warn">⚠ {$_('step4.devVM.invalidIPv4')}</span>
            {/if}
          </Field>
          <Field label={$_('step4.nameservers')} hint="1.1.1.1, 8.8.8.8">
            <input value={$wizardStore.inventory.network.nameservers.join(', ')}
                   class:input-error={invalidNameservers.length > 0}
                   oninput={(e) => setNameserversText((e.target as HTMLInputElement).value)} />
            {#if invalidNameservers.length > 0}
              <span class="input-warn">⚠ {$_('step4.devVM.invalidIPv4')}: <code>{invalidNameservers.join(', ')}</code></span>
            {/if}
          </Field>
        </div>
      {:else}
        <p class="muted dhcp-note">{$_('step4.devVM.dhcpNote')}</p>
        <Field label={$_('step4.node.ip')} hint={$_('step4.devVM.ipDHCPHint')}>
          <input value={devVMNode.ip}
                 oninput={(e) => updateDevVMNode({ ip: (e.target as HTMLInputElement).value })}
                 placeholder="10.10.1.50 (선택, verify 단계용)" />
        </Field>
      {/if}
      {#if devVMNode.primary_mac}
        <p class="muted mac-line">MAC: <code>{devVMNode.primary_mac}</code> <small>({$_('step4.devVM.macAuto')})</small></p>
      {/if}
    </div>

    <!-- Extra NIC rows -->
    {#each (devVMNode.nics ?? []).slice(1) as nic, idx}
      {@const realIdx = idx + 1}
      {@const nicMode = nic.ip_mode ?? 'dhcp'}
      {@const nicIPInvalid = nicMode === 'static' && !!nic.ip && !isValidIPv4(nic.ip)}
      {@const nicGwInvalid = nicMode === 'static' && !!nic.gateway && !isValidIPv4(nic.gateway)}
      <div class="extra-row">
        <div class="extra-row-head">
          <span class="row-badge muted">{$_('step4.devVM.extraNIC')} #{realIdx}</span>
          <button type="button" class="row-remove" onclick={() => removeDevVMNIC(realIdx)}
                  title={$_('step4.devVM.removeNIC')}>×</button>
        </div>
        <div class="grid-2">
          <Field label={$_('step4.devVM.nicNetwork')} hint={$_('step4.devVM.nicNetworkHint')} required>
            {#if ($wizardStore.discovered.networks ?? []).length > 0}
              <select value={nic.network ?? ''}
                      onchange={(e) => updateDevVMNIC(realIdx, { network: (e.target as HTMLSelectElement).value })}>
                <option value="">— {$_('step4.devVM.networkPicker')} —</option>
                {#each $wizardStore.discovered.networks ?? [] as net}
                  <option value={net.name}>{net.name}{net.vswitch ? ` (${net.vswitch})` : ''}</option>
                {/each}
              </select>
            {:else}
              <input value={nic.network ?? ''}
                     oninput={(e) => updateDevVMNIC(realIdx, { network: (e.target as HTMLInputElement).value })}
                     placeholder="VM Network" />
            {/if}
          </Field>
          <Field label={$_('step4.devVM.nicLabel')}>
            <input value={nic.label ?? ''}
                   placeholder="storage, mgmt, …"
                   oninput={(e) => updateDevVMNIC(realIdx, { label: (e.target as HTMLInputElement).value })} />
          </Field>
        </div>
        <div class="ipmode-row">
          <span class="ipmode-label">{$_('step4.devVM.ipMode')}</span>
          <label class="ipmode-radio">
            <input type="radio" name={`nicmode-${realIdx}`} checked={nicMode === 'dhcp'}
                   onchange={() => updateDevVMNIC(realIdx, { ip_mode: 'dhcp', ip: '', gateway: '' })} />
            <span>DHCP</span>
          </label>
          <label class="ipmode-radio">
            <input type="radio" name={`nicmode-${realIdx}`} checked={nicMode === 'static'}
                   onchange={() => updateDevVMNIC(realIdx, { ip_mode: 'static' })} />
            <span>{$_('step4.devVM.ipModeStatic')}</span>
          </label>
        </div>
        {#if nicMode === 'static'}
          <div class="grid-4">
            <Field label={$_('step4.node.ip')}>
              <input value={nic.ip ?? ''}
                     class:input-error={nicIPInvalid}
                     oninput={(e) => updateDevVMNIC(realIdx, { ip: (e.target as HTMLInputElement).value })} />
              {#if nicIPInvalid}
                <span class="input-warn">⚠ {$_('step4.devVM.invalidIPv4')}</span>
              {/if}
            </Field>
            <Field label={$_('step4.devVM.prefixLen')}>
              <input type="number" min="8" max="30"
                     value={nic.prefix_len ?? 24}
                     oninput={(e) => updateDevVMNIC(realIdx, { prefix_len: +(e.target as HTMLInputElement).value || 24 })} />
            </Field>
            <Field label={$_('step4.gateway')} hint={$_('step4.devVM.nicGatewayHint')}>
              <input value={nic.gateway ?? ''}
                     class:input-error={nicGwInvalid}
                     oninput={(e) => updateDevVMNIC(realIdx, { gateway: (e.target as HTMLInputElement).value })} />
              {#if nicGwInvalid}
                <span class="input-warn">⚠ {$_('step4.devVM.invalidIPv4')}</span>
              {/if}
            </Field>
            <Field label={$_('step4.nameservers')}>
              <input value={(nic.nameservers ?? []).join(', ')}
                     placeholder="(선택)"
                     oninput={(e) => updateDevVMNIC(realIdx, { nameservers: (e.target as HTMLInputElement).value.split(',').map((x) => x.trim()).filter(Boolean) })} />
            </Field>
          </div>
        {/if}
        {#if nic.mac}
          <p class="muted mac-line">MAC: <code>{nic.mac}</code> <small>({$_('step4.devVM.macAuto')})</small></p>
        {/if}
      </div>
    {/each}

    <div class="add-row">
      <Button variant="ghost" onclick={() => addDevVMNIC($wizardStore.inventory.target.network || 'VM Network')}>
        + {$_('step4.devVM.addNIC')}
      </Button>
    </div>

    <p class="muted">{$_('step4.devVM.note')}</p>
  </Section>

  <StepNav {canAdvance} />
{:else}

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

    {#if showK8s}
      <Section title="Kubernetes 구성"
               subtitle="클러스터 join 토큰, kube-vip 인터페이스, 추가 TLS SAN.">
        <div class="grid-2">
          <Field label="클러스터 join 토큰"
                 hint="K3s/RKE2 노드가 클러스터에 합류할 때 사용. 비워두면 설치 직전 자동 생성. 기존 토큰을 알면 그대로 입력해 추가 노드 join 가능.">
            <div class="row-with-btn">
              <input type="password"
                     value={$wizardStore.inventory.cluster.kubernetes.token ?? ''}
                     oninput={(e) => updateK8s({ token: (e.target as HTMLInputElement).value })}
                     placeholder="(설치 시 자동 생성)" />
              <Button variant="secondary" onclick={() => updateK8s({ token: generateToken() })}>
                생성
              </Button>
            </div>
          </Field>

          <Field label="kube-vip 인터페이스"
                 hint="kube-vip가 control-plane VIP를 ARP로 광고할 NIC 이름. ESXi vmxnet3는 ens192, libvirt virtio는 eth0/enp1s0.">
            <input value={$wizardStore.inventory.cluster.kubernetes.kube_vip_interface ?? ''}
                   oninput={(e) => updateK8s({ kube_vip_interface: (e.target as HTMLInputElement).value })}
                   placeholder="ens192" />
          </Field>
        </div>

        <Field label="추가 TLS SAN"
               hint={`API 서버 인증서에 추가될 SAN (쉼표 구분). VIP(${$wizardStore.inventory.network.vip || 'unset'}) + 노드 IP는 자동 포함되니 외부 DNS 이름만 추가: 예) k8s-prod.triangles.com, api.cluster.local`}>
          <input value={tlsSansText()}
                 oninput={(e) => setTLSSans((e.target as HTMLInputElement).value)}
                 placeholder="k8s-prod.example.com, api.cluster.local" />
        </Field>
      </Section>

      {#if topology === 'k8s-only'}
        <Section title="외부 Ceph 연결 (선택)"
                 subtitle="기존 Ceph 클러스터에 ceph-csi로 연결. K8s + 별도 운영 Ceph 패턴 (IDC 표준).">
          <label class="checkbox">
            <input type="checkbox"
                   checked={useExternalCeph}
                   onchange={(e) => toggleExternalCeph((e.target as HTMLInputElement).checked)} />
            <span>기존 Ceph 클러스터에 연결 (ceph-csi로 RBD/CephFS PVC 사용)</span>
          </label>

          {#if useExternalCeph && externalCeph}
            <div class="grid-2">
              <Field label="MON 엔드포인트"
                     hint="Ceph mon 데몬 v2 주소들 (쉼표 구분). 보통 :3300 포트. 예: 10.10.1.75:3300, 10.10.1.76:3300, 10.10.1.77:3300"
                     required>
                <input value={monsText()}
                       oninput={(e) => setMons((e.target as HTMLInputElement).value)}
                       placeholder="10.10.1.75:3300, 10.10.1.76:3300, 10.10.1.77:3300" />
              </Field>

              <Field label="FSID (Ceph cluster ID)"
                     hint="기존 Ceph에서 'ceph fsid' 명령어로 확인. UUID 형식."
                     required>
                <input value={externalCeph.fsid}
                       oninput={(e) => updateExternalCeph({ fsid: (e.target as HTMLInputElement).value })}
                       placeholder="a7f2c4e8-3b1d-4f9a-b2c5-7e8d9a1b3c4f" />
              </Field>

              <Field label="Pool name"
                     hint="ceph-csi가 RBD 이미지를 만들 풀. 보통 'rbd-pool' (IDC 기본). 외부 Ceph에 미리 생성돼 있어야 함.">
                <input value={externalCeph.pool}
                       oninput={(e) => updateExternalCeph({ pool: (e.target as HTMLInputElement).value })}
                       placeholder="rbd-pool" />
              </Field>

              <Field label="Client 사용자명"
                     hint="좁은 권한의 Ceph client (admin 사용 금지). IDC 표준: k8s-rbd / k8s-cephfs.">
                <input value={externalCeph.client_user}
                       oninput={(e) => updateExternalCeph({ client_user: (e.target as HTMLInputElement).value })}
                       placeholder="k8s-rbd" />
              </Field>
            </div>

            <Field label="Client keyring (base64 또는 raw key)"
                   hint="기존 Ceph에서 'ceph auth get-key client.k8s-rbd' 결과. ceph-csi Secret에 직접 들어감."
                   required>
              <input type="password"
                     value={externalCeph.client_key}
                     oninput={(e) => updateExternalCeph({ client_key: (e.target as HTMLInputElement).value })}
                     placeholder="AQDxxx..." />
            </Field>

            <p class="muted">
              Apply 단계에서 30-csi-ceph 플레이북이 이 정보로 ceph-csi-rbd Helm 차트를 설치합니다.
              새 Ceph 부트스트랩은 하지 않습니다.
            </p>
          {/if}
        </Section>
      {/if}
    {/if}

    {#if clusterIssues.length > 0}
      <Section title="검증" subtitle="클러스터 전반에 걸친 충돌·중복 검사">
        <div class="ceph-hints">
          {#each clusterIssues as h}
            <div class="hint-row hint-{h.tone}">
              <Badge tone={h.tone}>{h.tone === 'danger' ? '필수' : '권장'}</Badge>
              <span>{h.msg}</span>
            </div>
          {/each}
        </div>
      </Section>
    {/if}

    {#if showCeph}
      <Section title="Ceph 데몬 네트워크"
               subtitle="OSD 디스크 사이즈는 노드 행을 펼쳐서 노드별로 직접 지정 — 노드마다 다른 디스크 구성이 가능합니다. 풀 / replica / failure-domain 같은 Ceph 운영 결정은 아래 별도 섹션(접힘)에서 하거나 클러스터 부트스트랩 후 ceph 명령으로 조정.">
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

        <details class="cephadm-advanced">
          <summary>
            <span class="adv-title">Ceph 클러스터 운영 설정</span>
            <span class="adv-sub">cephadm 부트스트랩 시점에 적용 — VM 생성과 무관, 클러스터 가동 후 <code>ceph osd pool</code> / <code>ceph orch</code>로도 조정 가능</span>
          </summary>
          <div class="adv-body">
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

            <div class="grid-3">
              <Field label="복제 수 (replica)"
                     hint="RBD/CephFS 풀의 기본 replica. 3 = 표준 (host 2개 손실 OK), 2 = 랩 전용, 1 = 단일 노드만.">
                <select value={String($wizardStore.inventory.ceph.replication ?? 3)}
                        onchange={(e) => updateCeph({ replication: +(e.target as HTMLSelectElement).value })}>
                  <option value="1">1 (단일 노드)</option>
                  <option value="2">2 (랩 전용)</option>
                  <option value="3">3 (표준 — 권장)</option>
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
          </div>
        </details>

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

      <div class="sample-row">
        <span class="muted">샘플 인벤토리 (한 번에 채우기):</span>
        <button class="link-btn" onclick={() => loadSample('idc-full')} type="button">
          IDC 통합 (Ceph 8 + K8s 7)
        </button>
        <button class="link-btn" onclick={() => loadSample('idc-ceph')} type="button">
          IDC Ceph 전용 (8노드)
        </button>
        <button class="link-btn" onclick={() => loadSample('idc-k3s')} type="button">
          IDC K8s 전용 (cp 3 + worker 4)
        </button>
        <button class="link-btn" onclick={() => loadSample('lab-min')} type="button">
          최소 랩 (K3s 단일)
        </button>
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
            <Field label={$_('step4.node.clusterIP')}
                   hint={isCephCoreOnly(node.roles)
                     ? $_('step4.node.clusterIPDisabledHint')
                     : $_('step4.node.clusterIPHint')}>
              <input value={node.cluster_ip ?? ''}
                     disabled={isCephCoreOnly(node.roles)}
                     oninput={(e) => updateNode(i, { cluster_ip: (e.target as HTMLInputElement).value || undefined })} />
            </Field>
            <Field label="OS" hint={$_('step4.node.osReadOnlyHint')}>
              <div class="readonly-os">{osLabel(node.os)}</div>
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
            <Field label="VM 디스크 데이터스토어 (필수, {datastoreOptions.length} 개 발견)"
                   hint={datastoreOptions.length > 0
                     ? `이 노드의 root + extra 디스크가 떨어질 ESXi 데이터스토어. failure-domain 분산을 위해 OSD 노드들끼리 다른 데이터스토어로 분산 권장.`
                     : 'Step 2의 "연결 + 리소스 가져오기"를 먼저 실행하면 드롭다운이 채워집니다.'}
                   required>
              {#if datastoreOptions.length > 0}
                <select value={node.datastore ?? ''}
                        onchange={(e) => updateNode(i, { datastore: (e.target as HTMLSelectElement).value || undefined })}
                        class:invalid={!node.datastore}>
                  <option value="">— 데이터스토어 선택 —</option>
                  {#each datastoreOptions as ds}
                    <option value={ds.name}>{ds.name} ({ds.type ?? 'VMFS'}, {ds.free_gb ? ds.free_gb.toFixed(0) : '?'} / {ds.capacity_gb ? ds.capacity_gb.toFixed(0) : '?'} GB)</option>
                  {/each}
                </select>
              {:else}
                <input value={node.datastore ?? ''}
                       oninput={(e) => updateNode(i, { datastore: (e.target as HTMLInputElement).value || undefined })}
                       placeholder="ex: SSD-RAID0-4Ti-02"
                       class:invalid={!node.datastore} />
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
                       hint="cephadm이 BlueStore OSD data로 사용할 블록 디바이스. VM에 디바이스 개수만큼의 가상 디스크가 할당되며, 각 디스크 크기는 옆 칸에서 지정."
                       error={dataDevicesOf(node).length === 0 ? '최소 1개 필요 — cephadm bootstrap이 OSD를 만들지 못합니다' : ''}
                       required>
                  <input value={devicesText(dataDevicesOf(node))}
                         oninput={(e) => setDataDevices(i, (e.target as HTMLInputElement).value)}
                         placeholder="/dev/sdb, /dev/sdc" />
                </Field>
                <Field label="Data 디스크 크기 (GB / 디스크)"
                       hint={dataDevicesOf(node).length > 0
                         ? `→ ${dataDevicesOf(node).length} × ${node.osd_data_size_gb ?? 64} GB = ${dataDevicesOf(node).length * (node.osd_data_size_gb ?? 64)} GB 합계`
                         : '디바이스 개수와 곱해서 vmdk 할당'}>
                  <input type="number" min="1"
                         value={node.osd_data_size_gb ?? 64}
                         oninput={(e) => updateNode(i, { osd_data_size_gb: +(e.target as HTMLInputElement).value || undefined })} />
                </Field>
              </div>

              <div class="grid-2">
                <Field label="DB devices (BlueStore 메타, 선택)"
                       hint={(node.device_class ?? 'auto') === 'hdd'
                         ? '⚡ HDD에 강력 권장 — 별도 SSD 입력 시 throughput 4-8× 개선. 비워두면 data device에 함께 저장.'
                         : '선택 — 비워두면 data device에 함께 저장.'}>
                  <input value={devicesText(node.db_devices)}
                         oninput={(e) => setDBDevices(i, (e.target as HTMLInputElement).value)}
                         placeholder="/dev/sdc" />
                </Field>
                <Field label="DB 디스크 크기 (GB / 디스크)"
                       hint={(node.db_devices ?? []).length > 0
                         ? `→ ${(node.db_devices ?? []).length} × ${node.osd_db_size_gb ?? 16} GB`
                         : 'DB devices가 비어있으면 미할당'}>
                  <input type="number" min="0"
                         value={node.osd_db_size_gb ?? 16}
                         oninput={(e) => updateNode(i, { osd_db_size_gb: +(e.target as HTMLInputElement).value || undefined })} />
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
                  <Field label="WAL 디스크 크기 (GB / 디스크)"
                         hint="WAL devices가 비어있으면 무시됨.">
                    <input type="number" min="0"
                           value={node.osd_wal_size_gb ?? 0}
                           oninput={(e) => updateNode(i, { osd_wal_size_gb: +(e.target as HTMLInputElement).value || undefined })} />
                  </Field>
                </div>
              {/if}
            </div>
          {/if}
          {/if}
        </div>
      {/each}

      {#if $wizardStore.inventory.nodes.length === 0}
        <p class="muted empty-hint">위 프리셋 카드에서 노드를 추가하세요.</p>
      {/if}
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
{/if}

<style>
  .step-header { margin-bottom: 1.25rem; }
  .step-header h2 { margin: 0; font-size: 1.3rem; }
  .step-header p { margin: 0.25rem 0 0; color: #a1a1aa; font-size: 0.9rem; }

  .layout { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr); gap: 1rem; }
  @media (max-width: 1100px) { .layout { grid-template-columns: 1fr; } }

  .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0.75rem; }
  .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.5rem; }

  /* Read-only OS display — looks like a disabled input so users see it as
     informational rather than expecting it to be editable here. */
  .readonly-os { padding: 0.45rem 0.65rem; border: 1px solid #2a2a30;
                 border-radius: 4px; background: #16161a;
                 color: #d4d4d8; font-size: 0.85rem;
                 font-family: inherit; }

  /* Required field that's currently empty — red border to signal "fix me". */
  :global(input.invalid), :global(select.invalid) {
    border-color: #b91c1c !important;
    background: #1f0a0a;
  }

  /* Collapsible "deployment-time Ceph settings" — visually de-emphasised
     because these belong to the Ceph operator's runtime, not the wizard's
     OS provisioning step. Closed by default. */
  details.cephadm-advanced { margin-top: 1rem;
    border: 1px dashed #3f3f46; border-radius: 6px;
    background: #0a0a0c; }
  details.cephadm-advanced > summary {
    list-style: none; cursor: pointer; padding: 0.7rem 0.85rem;
    user-select: none; }
  details.cephadm-advanced > summary::before {
    content: '▶'; display: inline-block; margin-right: 0.5rem;
    color: #71717a; font-size: 0.7rem; transition: transform 0.1s; }
  details.cephadm-advanced[open] > summary::before { transform: rotate(90deg); }
  details.cephadm-advanced .adv-title { font-weight: 600; color: #d4d4d8;
    font-size: 0.9rem; }
  details.cephadm-advanced .adv-sub { display: block; margin-top: 0.2rem;
    margin-left: 1.4rem; font-size: 0.72rem; color: #71717a; line-height: 1.5; }
  details.cephadm-advanced .adv-sub code {
    background: #16161a; padding: 0.05rem 0.3rem; border-radius: 3px;
    font-family: ui-monospace, monospace; color: #93c5fd; }
  details.cephadm-advanced .adv-body { padding: 0.5rem 0.85rem 0.85rem;
    border-top: 1px dashed #2a2a30; }

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

  .empty-hint { padding: 1rem; text-align: center; background: #0f0f12;
                border: 1px dashed #3f3f46; border-radius: 6px; margin: 0; }
  .presets-head { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 0.6rem; }
  .sample-row { display: flex; gap: 0.4rem; align-items: center; flex-wrap: wrap;
                margin-bottom: 0.85rem; padding: 0.5rem 0.7rem;
                background: #0a0a0c; border: 1px dashed #3f3f46; border-radius: 5px; }
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

  .alloc-hint { display: block; font-size: 0.7rem; color: #fbbf24;
                font-family: ui-monospace, monospace; margin-top: 0.2rem; }
  .row-with-btn { display: flex; gap: 0.4rem; align-items: stretch; }
  .row-with-btn input { flex: 1; }

  .osd-defaults-head { font-size: 0.75rem; color: #f59e0b; font-weight: 600;
                       text-transform: uppercase; letter-spacing: 0.05em;
                       margin: 0.75rem 0 0.4rem; padding-top: 0.6rem;
                       border-top: 1px solid #2a2a30; }
  .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.6rem; }

  /* dev-vm specific UI */
  .ipmode-row { display: grid; grid-template-columns: 8rem 1fr 1fr; gap: 0.5rem;
                align-items: stretch; margin: 0.6rem 0 0.4rem; }
  .ipmode-label { color: #71717a; font-size: 0.78rem; text-transform: uppercase;
                  letter-spacing: 0.05em; align-self: center; }
  .ipmode-radio { display: grid; grid-template-rows: auto auto; gap: 0.15rem;
                  padding: 0.5rem 0.7rem; background: #0f0f12;
                  border: 1px solid #2a2a30; border-radius: 5px; cursor: pointer; }
  .ipmode-radio:hover { border-color: #52525b; }
  .ipmode-radio input { display: none; }
  .ipmode-radio span { display: flex; align-items: center; gap: 0.4rem;
                       font-size: 0.88rem; color: #e4e4e7; }
  .ipmode-radio span::before { content: '○'; color: #71717a; font-size: 1rem; line-height: 1; }
  .ipmode-radio:has(input:checked) { border-color: #3b82f6; background: #1e293b; }
  .ipmode-radio:has(input:checked) span::before { content: '●'; color: #60a5fa; }
  .ipmode-radio small { color: #71717a; font-size: 0.72rem; line-height: 1.4; padding-left: 1.4rem; }

  .dhcp-note { background: #1e293b; border: 1px solid #1e3a8a; padding: 0.5rem 0.7rem;
               border-radius: 5px; color: #cbd5e1; font-size: 0.82rem;
               margin: 0.5rem 0; }

  .input-error { border-color: #dc2626 !important;
                 box-shadow: 0 0 0 1px #dc2626 inset; }
  .input-warn { display: block; color: #fca5a5; font-size: 0.75rem;
                margin-top: 0.25rem; line-height: 1.4; }
  .input-warn code { background: #1f1010; color: #fca5a5; padding: 0.05rem 0.3rem;
                     border-radius: 3px; font-family: ui-monospace, monospace; }

  /* dev-vm: multi-disk / multi-NIC dynamic rows */
  .grid-4 { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.5rem; }
  @media (max-width: 800px) { .grid-4 { grid-template-columns: 1fr 1fr; } }

  .multi-row-header { margin: 1rem 0 0.4rem; padding-top: 0.7rem;
                      border-top: 1px solid #2a2a30; }
  .multi-row-header h4 { margin: 0; font-size: 0.85rem; color: #e4e4e7;
                         text-transform: uppercase; letter-spacing: 0.05em;
                         display: flex; align-items: center; gap: 0.5rem; }
  .multi-row-header p { margin: 0.2rem 0 0; font-size: 0.72rem; color: #71717a; }

  .row-badge { font-size: 0.65rem; padding: 0.05rem 0.45rem; border-radius: 3px;
               background: #1e293b; color: #93c5fd; border: 1px solid #1e3a8a;
               font-family: ui-monospace, monospace; letter-spacing: 0.04em;
               font-weight: 600; }
  .row-badge.muted { background: #1a1a1f; color: #a1a1aa; border-color: #3f3f46; }

  .extra-row { margin: 0.55rem 0; padding: 0.55rem 0.75rem 0.7rem;
               background: #0f0f12; border: 1px solid #2a2a30;
               border-radius: 5px; }
  .extra-row-head { display: flex; gap: 0.5rem; align-items: center;
                    margin-bottom: 0.4rem; }
  .row-remove { margin-left: auto; background: transparent; border: 1px solid #3f3f46;
                color: #a1a1aa; border-radius: 4px; cursor: pointer;
                width: 1.5rem; height: 1.5rem; line-height: 1; padding: 0;
                font-size: 0.95rem; font-family: inherit; }
  .row-remove:hover { border-color: #b91c1c; color: #fca5a5; background: #1f0a0a; }

  .add-row { margin-top: 0.4rem; }
  .mac-line { margin: 0.4rem 0 0; font-size: 0.72rem; }
  .mac-line code { background: #16161a; padding: 0.05rem 0.3rem; border-radius: 3px;
                   font-family: ui-monospace, monospace; color: #93c5fd; }

  /* Disabled input with the same border/padding so the row stays
     visually aligned (used for the "OS"/"primary" label cells). */
  :global(input.readonly-input) {
    background: #0a0a0c !important;
    color: #71717a !important;
    cursor: not-allowed;
  }
</style>
