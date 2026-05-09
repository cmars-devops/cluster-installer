import { writable } from 'svelte/store';

export type Role =
  | 'control-plane' | 'etcd' | 'worker'
  | 'ceph-mon' | 'ceph-mgr' | 'ceph-osd' | 'ceph-mds' | 'ceph-rgw';

export type DeviceClass = 'auto' | 'hdd' | 'ssd' | 'nvme';

// DiskSpec / NICSpec mirror inventory.DiskSpec / inventory.NICSpec on
// the Go side. Used by dev-vm Step 4 to populate node.disks / node.nics
// directly. Cluster mode leaves both empty — tfvars renderer falls back
// to legacy DiskGB / single-NIC fields via EffectiveDisks/EffectiveNICs.
export interface DiskSpec {
  size_gb: number;
  datastore?: string;
  provisioning?: 'thin' | 'thick' | 'thick-eager';
  label?: string;
}
export interface NICSpec {
  network: string;                         // ESXi port-group name
  ip_mode?: 'static' | 'dhcp';
  ip?: string;
  prefix_len?: number;
  gateway?: string;
  nameservers?: string[];
  mac?: string;                             // pre-allocated by orchestrator
  label?: string;
}

export interface NodeSpec {
  hostname: string;
  /** vSphere inventory label. Empty = reuse hostname (most common). Set
   *  this when you want vSphere to show e.g. "DEV-DEVVM-01" while the
   *  guest OS still reports the plain "devvm-01". */
  display_name?: string;
  ip: string;
  cluster_ip?: string;
  roles: Role[];
  os: 'microos' | 'leap' | 'tumbleweed' | 'ubuntu';
  /** Optional pinned OS version. For Ubuntu: '24.04' or '26.04'. */
  os_version?: string;
  /** Multi-disk override. Position 0 = OS install disk; rest = blank
   *  extras. When empty, the Go side falls back to disk_gb +
   *  cluster Ceph helpers via EffectiveDisks(). */
  disks?: DiskSpec[];
  /** Multi-NIC override. Position 0 = primary (verify dials its IP).
   *  When empty, the Go side synthesises a single NIC from
   *  target.network + per-node IP/PrimaryMAC via EffectiveNICs(). */
  nics?: NICSpec[];
  /** "static" (default) or "dhcp". When "dhcp", IP / gateway fields are
   *  ignored at install time — autoinstall enables dhcp4 on the primary NIC. */
  ip_mode?: 'static' | 'dhcp';
  /** Per-node SSH public keys (dev-vm mode lets the user paste keys directly). */
  ssh_authorized_keys?: string[];
  /** Pre-allocated MAC (deterministic from cluster_name + hostname; read-only). */
  primary_mac?: string;
  cpu: number;
  memory_gb: number;
  disk_gb: number;

  // ── OSD-specific (only meaningful when roles includes 'ceph-osd') ─────
  /** Devices that hold OSD data (BlueStore data partition).
   *  Typically HDDs in a hybrid setup, or SSDs/NVMe in an all-flash one.
   *  Required for ceph-osd nodes.
   *  Example: ['/dev/sdb', '/dev/sdc'] */
  data_devices?: string[];
  /** Size (GB) of each data disk on THIS node. Disk count = data_devices length;
   *  every entry gets allocated `osd_data_size_gb` GB. Per-node so heterogeneous
   *  OSD clusters (e.g. some hosts with bigger HDDs) are expressible without a
   *  cluster-wide default. */
  osd_data_size_gb?: number;
  osd_db_size_gb?: number;
  osd_wal_size_gb?: number;
  /** Optional: devices that hold BlueStore DB (rocksdb). Strongly
   *  recommended when data_devices are HDDs — placing DB on SSD/NVMe
   *  improves OSD throughput 4-8×. cephadm will partition these
   *  automatically across the OSDs sharing them.
   *  Example: ['/dev/nvme0n1'] */
  db_devices?: string[];
  /** Optional separate WAL location. Rare — usually shares db_devices. */
  wal_devices?: string[];
  /** OSD daemons per device. Default 1. Set higher for very-high-IOPS
   *  NVMe to keep all queues busy. */
  osds_per_device?: number;
  /** dm-crypt encryption-at-rest for OSD data. Default false. */
  osd_encrypted?: boolean;
  /** CRUSH device class — used by Ceph rules to differentiate fast vs
   *  slow tiers. 'auto' lets ceph detect from rotational flag. */
  device_class?: DeviceClass;

  /** @deprecated Backward compat alias for data_devices. New inventories
   *  should use data_devices/db_devices instead. */
  storage_devices?: string[];
  /** Per-node placement override — which datastore (ESXi/Proxmox) or
   *  storage pool (libvirt) holds this VM's virtual disks.
   *  Blank = use the cluster-level target.datastore. */
  datastore?: string;
  /** Disk provisioning strategy:
   *   - 'thin'         — sparse / on-demand allocation. Storage efficient.
   *                      libvirt: qcow2  /  Proxmox: file_format=qcow2
   *                      ESXi: thin
   *   - 'thick'        — fully pre-allocated, lazy zeroing.
   *                      libvirt: raw  /  Proxmox: file_format=raw
   *                      ESXi: zeroedthick (lazy)
   *   - 'thick-eager'  — pre-allocated + zeroed at create. Best for Ceph OSDs
   *                      (avoids first-write penalty on data disks). ESXi-
   *                      specific; on libvirt/Proxmox falls back to 'thick'.
   *  Default: 'thin'. */
  disk_provisioning?: 'thin' | 'thick' | 'thick-eager';
}

// dev-vm = single-VM unattended-install verification mode (no cluster).
export type Topology = 'ceph-only' | 'k8s-only' | 'combined' | 'dev-vm';

export interface Inventory {
  cluster: {
    name: string;
    domain: string;
    timezone: string;
    topology: Topology;
    kubernetes: {
      distro: 'rke2' | 'k3s';
      version: string;
      cni: 'cilium' | 'canal' | 'calico';
      /** Cluster join token (k3s/rke2). Generated once per cluster — used
       *  for adding more servers/agents later. Treat as a secret. */
      token?: string;
      /** Network interface name kube-vip ARP-advertises the VIP on. ESXi
       *  vmxnet3: 'ens192'; libvirt virtio: 'eth0' or 'enp1s0'. Leave
       *  blank for auto-detect (kube-vip picks first up-interface). */
      kube_vip_interface?: string;
      /** Extra TLS SANs for the API server cert (comma-list of DNS names
       *  / IPs). VIP and node IPs are added automatically; this is for
       *  external DNS records like 'k8s-prod.triangles.com'. */
      tls_sans?: string[];
    };
    external_ceph?: {
      // Used when topology=k8s-only AND user wants ceph-csi to connect to an
      // existing Ceph cluster. Captured in Step 4.
      mon_endpoints: string[];
      fsid: string;
      client_user: string;       // typically "k8s-rbd"
      client_key: string;        // base64-encoded keyring secret
      pool: string;              // typically "rbd-pool"
    };
  };
  network: {
    pod_cidr: string;
    service_cidr: string;
    vip: string;
    lb_pool: string;
    ingress_lb_ip: string;
    gateway: string;
    nameservers: string[];
    prefix_len: number;
    cluster_prefix_len: number;
  };
  target: {
    type: 'libvirt' | 'proxmox' | 'esxi';
    endpoint: string;
    username: string;          // default 'root'
    ssh_key: string;           // libvirt always; ESXi optional
    api_token: string;         // proxmox
    password: string;          // ESXi root password (used for vSphere API + SSH)
    datastore: string;         // ESXi datastore for VM disks/seed ISOs
    iso_datastore: string;     // ESXi datastore for ISO uploads (often same)
    network: string;           // ESXi port group name
    tls_insecure: boolean;
    advertise_ip: string;
  };
  nodes: NodeSpec[];
  ceph: {
    mode: 'external' | 'rook';
    public_network: string;
    cluster_network: string;
    pools: string[];
    /** Replica count for the default RBD/CephFS pools. 3 = standard
     *  fault tolerance; 2 trades safety for capacity (only viable in
     *  small labs); 1 = no replication, single-node only. */
    replication?: number;
    /** CRUSH failure domain — Ceph spreads replicas across distinct
     *  units of this type. 'host' is the default and works for most
     *  clusters. 'rack'/'chassis' need CRUSH topology configured. */
    failure_domain?: 'host' | 'rack' | 'chassis' | 'osd';
    /** Default for OSDs that don't override per-node. */
    default_osds_per_device?: number;
    /** Default dm-crypt encryption for OSDs. */
    default_encrypted?: boolean;

    /** OSD virtual-disk sizes — applied uniformly across all OSD VMs. The
     *  number of allocated disks per node = the count of paths in the
     *  matching device list:
     *
     *  data_devices: ['/dev/sdb', '/dev/sdc']  →  2 disks × osd_data_disk_size_gb
     *  db_devices:   ['/dev/sdd']              →  1 disk  × osd_db_disk_size_gb
     *  wal_devices:  []                         →  0 disks (when WAL co-locates with DB)
     *
     *  For a typical HDD setup (IDC default): 2048 GB data + 100 GB DB.
     *  Ceph BlueStore guidance: DB should be ~1-4% of data size, with a
     *  minimum of ~30 GiB; 100 GB on a 2 TB HDD is mid-range and safe. */
    osd_data_disk_size_gb?: number;
    osd_db_disk_size_gb?: number;
    osd_wal_disk_size_gb?: number;
  };
  addons: { ingress: 'ingress-nginx' | 'traefik' | 'none'; cert_manager: boolean;
            monitoring: 'kube-prometheus-stack' | 'none'; gitops: 'argocd' | 'flux' | 'none' };
  content: { ref: string; repo: string };
  /** Cluster-wide credentials baked into the autoinstall user-data so newly
   *  installed nodes are reachable via SSH (and optionally console) without
   *  extra steps. SSH keys are paste-from-clipboard; node_password is a
   *  cluster-wide root password (lab convenience — production should use
   *  SSH keys only). */
  cluster_auth: {
    /** Sudo account name autoinstall creates on every node. SSH keys, the
     *  optional console password, and the sudoers NOPASSWD entry all
     *  attach to this account. Default: 'triangles'. */
    username: string;
    ssh_import_github: string[];   // GitHub usernames; keys auto-imported via ssh-import-id-gh
    ssh_authorized_keys: string[]; // raw keys pasted directly
    node_password: string;
  };
}

export interface DiscoveredResources {
  datastores?: Array<{ name: string; type?: string; free_gb?: number; capacity_gb?: number; accessible?: boolean }>;
  networks?: Array<{ name: string; vswitch?: string; vlan_id?: number }>;
  host?: { name: string; version: string; build: string; api_type: string };
}

export interface WizardState {
  step: number;
  // Top-level entry mode picked on Step 1.
  //   new-cluster | new       — multi-node Ceph/K8s install (legacy 'new' is alias)
  //   new-vm                  — single-VM unattended-install verification (dev-vm topology)
  //   resume                  — pick up an in-progress run from %LOCALAPPDATA%
  mode: 'new' | 'new-cluster' | 'new-vm' | 'resume';
  runId: string | null;
  contentDir: string | null;
  inventory: Inventory;
  discovered: DiscoveredResources;   // populated by Step 2 ESXi discovery; consumed by Step 4
  errors: string[];
  // OS preferences picked in Step 3 — kept separate from inventory.nodes
  // so they survive when the user navigates Step 3 ↔ Step 4 ↔ Step 1
  // even before any nodes are added. Step 4 presets read these to fill
  // in node.os when the user adds new nodes.
  osPreferences: {
    k8s:  NodeSpec['os'];
    ceph: NodeSpec['os'];
  };
}

const defaultInventory: Inventory = {
  cluster: {
    name: 'demo-cluster',
    domain: 'cluster.local',
    timezone: 'Asia/Seoul',
    topology: 'k8s-only',         // safest default — Ceph adds substantial complexity
    kubernetes: {
      distro: 'rke2',
      version: 'v1.31.4+rke2r1',
      cni: 'cilium',
      token: '',
      kube_vip_interface: 'ens192',
      tls_sans: []
    }
  },
  network: {
    pod_cidr: '10.42.0.0/16',
    service_cidr: '10.43.0.0/16',
    vip: '10.10.1.30',
    lb_pool: '10.10.1.41-10.10.1.49',
    ingress_lb_ip: '10.10.1.40',
    gateway: '10.10.1.1',
    nameservers: ['10.10.1.3'],
    prefix_len: 24,
    cluster_prefix_len: 24
  },
  target: {
    type: 'libvirt',
    endpoint: '',
    username: 'root',
    ssh_key: '',
    api_token: '',
    password: '',
    datastore: '',
    iso_datastore: '',
    network: 'VM Network',
    tls_insecure: true,  // ESXi/Proxmox lab installs almost always use self-signed certs; defaulting to true matches reality and avoids "x509: certificate signed by unknown authority" on first connect.
    advertise_ip: ''
  },
  nodes: [],
  ceph: {
    mode: 'external',
    public_network: '10.10.1.0/24',
    cluster_network: '172.16.1.0/24',
    pools: ['rbd', 'cephfs'],
    replication: 3,
    failure_domain: 'host',
    default_osds_per_device: 1,
    default_encrypted: false,
    // Defaults are conservative — sized for first install / smoke test.
    // Production operators bump these up explicitly before Apply.
    osd_data_disk_size_gb: 64,
    osd_db_disk_size_gb: 16,
    osd_wal_disk_size_gb: 0
  },
  addons: {
    ingress: 'ingress-nginx',
    cert_manager: true,
    monitoring: 'kube-prometheus-stack',
    gitops: 'none'
  },
  content: { ref: 'v0.1.0', repo: 'https://github.com/cmars-devops/cluster-installer-content.git' },
  cluster_auth: { username: 'triangles', ssh_import_github: [], ssh_authorized_keys: [], node_password: '' }
};

export const wizardStore = writable<WizardState>({
  step: 0,
  mode: 'new',
  runId: null,
  contentDir: null,
  inventory: defaultInventory,
  discovered: {},
  errors: [],
  osPreferences: { k8s: 'microos', ceph: 'leap' }
});

export function gotoStep(idx: number) {
  wizardStore.update((s) => ({ ...s, step: Math.max(0, Math.min(6, idx)) }));
}

// Each helper produces a fully new state tree. Svelte's $derived /
// $effect rely on reference identity for some optimization paths in
// runes mode, so mutating in place + returning the same `s` reference
// can leave reactive consumers stale (Step 2's target.type was the
// canonical bug).
export function addNode(template?: Partial<NodeSpec>) {
  wizardStore.update((s) => {
    const idx = s.inventory.nodes.length + 1;
    const fresh: NodeSpec = {
      hostname: template?.hostname ?? `node-${String(idx).padStart(2, '0')}`,
      ip: template?.ip ?? '',
      roles: template?.roles ?? ['worker'],
      os: template?.os ?? 'microos',
      cpu: template?.cpu ?? 4,
      memory_gb: template?.memory_gb ?? 8,
      disk_gb: template?.disk_gb ?? 60,
      ...template
    };
    return { ...s, inventory: { ...s.inventory, nodes: [...s.inventory.nodes, fresh] } };
  });
}

export function removeNode(index: number) {
  wizardStore.update((s) => ({
    ...s,
    inventory: { ...s.inventory, nodes: s.inventory.nodes.filter((_, i) => i !== index) }
  }));
}

export function updateNode(index: number, patch: Partial<NodeSpec>) {
  wizardStore.update((s) => ({
    ...s,
    inventory: {
      ...s.inventory,
      nodes: s.inventory.nodes.map((n, i) => (i === index ? { ...n, ...patch } : n))
    }
  }));
}

// ── dev-vm: multi-disk / multi-NIC helpers ────────────────────────────
// dev-vm node lives at inventory.nodes[0]. When the operator adds even
// one extra disk or NIC we materialize the FULL list (primary + extras)
// onto NodeSpec.disks / NodeSpec.nics, because the Go side's
// EffectiveDisks/EffectiveNICs treats a non-empty list as "the operator
// took control — use this verbatim, ignore legacy fields". Mixing
// primary-from-legacy + extras-from-array is not supported there.
//
// All helpers below preserve that invariant: any time .disks/.nics is
// non-empty after the call, .[0] reflects the legacy primary fields, and
// removing the last extra clears the array entirely so the legacy
// fallback resumes for cluster-mode parity.

function synthPrimaryDisk(n: NodeSpec): DiskSpec {
  return {
    size_gb: n.disk_gb || 40,
    datastore: n.datastore ?? '',
    provisioning: n.disk_provisioning ?? 'thin',
    label: 'OS'
  };
}
function synthPrimaryNIC(n: NodeSpec, fallbackNetwork: string): NICSpec {
  return {
    network: fallbackNetwork,
    ip_mode: n.ip_mode ?? 'static',
    ip: n.ip ?? '',
    mac: n.primary_mac,
    label: 'primary'
  };
}

export function addDevVMDisk() {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node) return s;
    const cur = node.disks ?? [];
    const next: DiskSpec[] = cur.length === 0
      ? [synthPrimaryDisk(node), { size_gb: 100, datastore: node.datastore ?? '', provisioning: node.disk_provisioning ?? 'thin', label: '' }]
      : [...cur, { size_gb: 100, datastore: cur[0].datastore ?? '', provisioning: cur[0].provisioning ?? 'thin', label: '' }];
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, disks: next } : n))
      }
    };
  });
}

export function removeDevVMDisk(idx: number) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node || !node.disks) return s;
    const filtered = node.disks.filter((_, i) => i !== idx);
    // Only the primary left → clear array so legacy fallback resumes.
    const next = filtered.length <= 1 ? undefined : filtered;
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, disks: next } : n))
      }
    };
  });
}

export function updateDevVMDisk(idx: number, patch: Partial<DiskSpec>) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node || !node.disks) return s;
    const next = node.disks.map((d, i) => (i === idx ? { ...d, ...patch } : d));
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, disks: next } : n))
      }
    };
  });
}

export function addDevVMNIC(fallbackNetwork: string) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node) return s;
    const cur = node.nics ?? [];
    const blank: NICSpec = { network: fallbackNetwork, ip_mode: 'dhcp', label: '' };
    const next: NICSpec[] = cur.length === 0
      ? [synthPrimaryNIC(node, fallbackNetwork), blank]
      : [...cur, blank];
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, nics: next } : n))
      }
    };
  });
}

export function removeDevVMNIC(idx: number) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node || !node.nics) return s;
    const filtered = node.nics.filter((_, i) => i !== idx);
    const next = filtered.length <= 1 ? undefined : filtered;
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, nics: next } : n))
      }
    };
  });
}

export function updateDevVMNIC(idx: number, patch: Partial<NICSpec>) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node || !node.nics) return s;
    const next = node.nics.map((nic, i) => (i === idx ? { ...nic, ...patch } : nic));
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, nics: next } : n))
      }
    };
  });
}

// syncPrimaryFromLegacy keeps disks[0] / nics[0] in lockstep with the
// legacy primary fields when the array is populated. Step 4 calls this
// after every primary-form mutation so adding extras doesn't fork the
// "source of truth" for the OS disk / primary NIC.
export function syncPrimaryFromLegacy(fallbackNetwork: string) {
  wizardStore.update((s) => {
    const node = s.inventory.nodes[0];
    if (!node) return s;
    let nextDisks = node.disks;
    let nextNICs = node.nics;
    if (node.disks && node.disks.length > 0) {
      const primary = synthPrimaryDisk(node);
      nextDisks = [{ ...node.disks[0], ...primary }, ...node.disks.slice(1)];
    }
    if (node.nics && node.nics.length > 0) {
      const primary = synthPrimaryNIC(node, fallbackNetwork);
      nextNICs = [{ ...node.nics[0], ...primary }, ...node.nics.slice(1)];
    }
    if (nextDisks === node.disks && nextNICs === node.nics) return s;
    return {
      ...s,
      inventory: {
        ...s.inventory,
        nodes: s.inventory.nodes.map((n, i) => (i === 0 ? { ...n, disks: nextDisks, nics: nextNICs } : n))
      }
    };
  });
}
