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
  const osdNodesWithoutDevices = $derived(osdNodes.filter(n => !n.storage_devices || n.storage_devices.length === 0));

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
        msg: `OSD 노드 ${osdNodesWithoutDevices.length}개에 storage_devices 미설정 — cephadm이 디스크를 찾지 못합니다. 각 OSD 행의 'Storage devices' 필드에 /dev/sdb 형식으로 입력.` });
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

  function addPreset(preset: 'k3s-1' | 'rke2-3' | 'ceph-core-3' | 'ceph-osd-3' | 'ceph-rgw-2') {
    if (preset === 'k3s-1') {
      addNode({ hostname: 'k3s-server-01', ip: '10.10.1.31',
                roles: ['control-plane', 'etcd', 'worker'], os: 'microos' });
    }
    if (preset === 'rke2-3') {
      const base = 31;
      ['cp1', 'cp2', 'cp3'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`, roles: ['control-plane', 'etcd'],
        os: 'microos', cpu: 4, memory_gb: 8, disk_gb: 60
      }));
    }
    // ── Ceph presets — IDC-validated layout (mon×3 + osd×N + rgw×2) ──
    if (preset === 'ceph-core-3') {
      const base = 75;
      ['ceph-core-01', 'ceph-core-02', 'ceph-core-03'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`,
        roles: ['ceph-mon', 'ceph-mgr', 'ceph-mds'], os: 'leap',
        cpu: 4, memory_gb: 8, disk_gb: 64
      }));
    }
    if (preset === 'ceph-osd-3') {
      const base = 91;
      ['ceph-osd-01', 'ceph-osd-02', 'ceph-osd-03'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`,
        cluster_ip: `172.16.1.${base + i}`,
        roles: ['ceph-osd'], os: 'leap',
        cpu: 4, memory_gb: 6, disk_gb: 64,
        storage_devices: ['/dev/sdb', '/dev/sdc']  // sdb=data, sdc=WAL/DB
      }));
    }
    if (preset === 'ceph-rgw-2') {
      const base = 81;
      ['ceph-rgw-01', 'ceph-rgw-02'].forEach((h, i) => addNode({
        hostname: h, ip: `10.10.1.${base + i}`,
        roles: ['ceph-rgw'], os: 'leap',
        cpu: 2, memory_gb: 4, disk_gb: 64
      }));
    }
  }

  function storageDevicesText(n: NodeSpec): string {
    return (n.storage_devices ?? []).join(', ');
  }
  function setStorageDevices(idx: number, text: string) {
    const list = text.split(',').map(s => s.trim()).filter(Boolean);
    updateNode(idx, { storage_devices: list });
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

      <div class="presets">
        <span class="muted">Presets:</span>
        {#if topology !== 'ceph-only'}
          <Button onclick={() => addPreset('k3s-1')}>+ K3s 단일 노드</Button>
          <Button onclick={() => addPreset('rke2-3')}>+ RKE2 control-plane × 3</Button>
        {/if}
        {#if topology !== 'k8s-only'}
          <Button onclick={() => addPreset('ceph-core-3')}>+ Ceph CORE × 3 (mon/mgr/mds)</Button>
          <Button onclick={() => addPreset('ceph-osd-3')}>+ Ceph OSD × 3 (data + WAL)</Button>
          <Button onclick={() => addPreset('ceph-rgw-2')}>+ Ceph RGW × 2 (S3)</Button>
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
            <Field label="Storage devices (OSD 데이터 디스크)"
                   hint="cephadm이 BlueStore OSD로 사용할 블록 디바이스 목록 (쉼표 구분). 첫 번째=data, 두 번째=WAL/DB. 예: /dev/sdb, /dev/sdc. 이 디스크들도 위 '설치 디스크 위치'와 같은 데이터스토어에 생성됩니다."
                   error={(node.storage_devices ?? []).length === 0 ? '최소 1개 디바이스 필요 — 비어있으면 cephadm bootstrap이 OSD를 생성할 수 없습니다' : ''}
                   required>
              <input value={storageDevicesText(node)}
                     oninput={(e) => setStorageDevices(i, (e.target as HTMLInputElement).value)}
                     placeholder="/dev/sdb, /dev/sdc" />
            </Field>
          {/if}
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
</style>
