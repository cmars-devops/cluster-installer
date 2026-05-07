import { writable } from 'svelte/store';

export type Role =
  | 'control-plane' | 'etcd' | 'worker'
  | 'ceph-mon' | 'ceph-mgr' | 'ceph-osd' | 'ceph-mds' | 'ceph-rgw';

export type DeviceClass = 'auto' | 'hdd' | 'ssd' | 'nvme';

export interface NodeSpec {
  hostname: string;
  ip: string;
  cluster_ip?: string;
  roles: Role[];
  os: 'microos' | 'leap' | 'tumbleweed';
  cpu: number;
  memory_gb: number;
  disk_gb: number;

  // ── OSD-specific (only meaningful when roles includes 'ceph-osd') ─────
  /** Devices that hold OSD data (BlueStore data partition).
   *  Typically HDDs in a hybrid setup, or SSDs/NVMe in an all-flash one.
   *  Required for ceph-osd nodes.
   *  Example: ['/dev/sdb', '/dev/sdc'] */
  data_devices?: string[];
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

export type Topology = 'ceph-only' | 'k8s-only' | 'combined';

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
}

export interface DiscoveredResources {
  datastores?: Array<{ name: string; type?: string; free_gb?: number; capacity_gb?: number; accessible?: boolean }>;
  networks?: Array<{ name: string; vswitch?: string; vlan_id?: number }>;
  host?: { name: string; version: string; build: string; api_type: string };
}

export interface WizardState {
  step: number;
  mode: 'new' | 'resume';
  runId: string | null;
  contentDir: string | null;
  inventory: Inventory;
  discovered: DiscoveredResources;   // populated by Step 2 ESXi discovery; consumed by Step 4
  errors: string[];
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
    nameservers: ['10.10.1.1', '8.8.8.8'],
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
    tls_insecure: false,
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
    osd_data_disk_size_gb: 2048,
    osd_db_disk_size_gb: 100,
    osd_wal_disk_size_gb: 0
  },
  addons: {
    ingress: 'ingress-nginx',
    cert_manager: true,
    monitoring: 'kube-prometheus-stack',
    gitops: 'none'
  },
  content: { ref: 'v0.1.0', repo: 'https://github.com/cmars-devops/cluster-installer-content.git' }
};

export const wizardStore = writable<WizardState>({
  step: 0,
  mode: 'new',
  runId: null,
  contentDir: null,
  inventory: defaultInventory,
  discovered: {},
  errors: []
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
