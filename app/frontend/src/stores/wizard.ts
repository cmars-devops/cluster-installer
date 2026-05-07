import { writable } from 'svelte/store';

export type Role =
  | 'control-plane' | 'etcd' | 'worker'
  | 'ceph-mon' | 'ceph-mgr' | 'ceph-osd' | 'ceph-mds' | 'ceph-rgw';

export interface NodeSpec {
  hostname: string;
  ip: string;
  cluster_ip?: string;
  roles: Role[];
  os: 'microos' | 'leap' | 'tumbleweed';
  cpu: number;
  memory_gb: number;
  disk_gb: number;
  storage_devices?: string[];
}

export type Topology = 'ceph-only' | 'k8s-only' | 'combined';

export interface Inventory {
  cluster: {
    name: string;
    domain: string;
    timezone: string;
    topology: Topology;
    kubernetes: { distro: 'rke2' | 'k3s'; version: string; cni: 'cilium' | 'canal' | 'calico' };
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
  ceph: { mode: 'external' | 'rook'; public_network: string; cluster_network: string; pools: string[] };
  addons: { ingress: 'ingress-nginx' | 'traefik' | 'none'; cert_manager: boolean;
            monitoring: 'kube-prometheus-stack' | 'none'; gitops: 'argocd' | 'flux' | 'none' };
  content: { ref: string; repo: string };
}

export interface WizardState {
  step: number;
  mode: 'new' | 'resume';
  runId: string | null;
  contentDir: string | null;
  inventory: Inventory;
  errors: string[];
}

const defaultInventory: Inventory = {
  cluster: {
    name: 'demo-cluster',
    domain: 'cluster.local',
    timezone: 'Asia/Seoul',
    topology: 'k8s-only',         // safest default — Ceph adds substantial complexity
    kubernetes: { distro: 'rke2', version: 'v1.31.4+rke2r1', cni: 'cilium' }
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
    pools: ['rbd', 'cephfs']
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
