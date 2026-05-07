import { writable } from 'svelte/store';

export interface Inventory {
  cluster: { name: string; domain: string; kubernetes: { distro: 'rke2' | 'k3s'; version: string; cni: string } };
  network: { pod_cidr: string; service_cidr: string; vip: string; lb_pool: string; gateway?: string; dns?: string; prefix_len?: number };
  target:  { type: 'libvirt' | 'proxmox'; endpoint: string; ssh_key?: string; username?: string; api_token?: string; tls_insecure?: boolean };
  nodes:   Array<{ hostname: string; ip: string; roles: string[]; os: 'microos' | 'leap' | 'tumbleweed';
                   cpu?: number; memory_gb?: number; disk_gb?: number; storage_devices?: string[] }>;
  ceph:    { mode: 'external' | 'rook'; public_network: string; cluster_network?: string; pools: string[] };
  addons:  { ingress?: string; cert_manager?: boolean; monitoring?: string; gitops?: string };
  content: { ref: string; repo?: string };
}

export interface WizardState {
  step: number;
  runId: string | null;
  contentRef: string;
  contentDir: string | null;
  inventory: Partial<Inventory>;
  errors: string[];
}

const defaultInventory: Partial<Inventory> = {
  cluster: { name: '', domain: 'cluster.local', kubernetes: { distro: 'rke2', version: 'v1.31.4+rke2r1', cni: 'cilium' } },
  network: { pod_cidr: '10.42.0.0/16', service_cidr: '10.43.0.0/16', vip: '', lb_pool: '' },
  target:  { type: 'libvirt', endpoint: '' },
  nodes:   [],
  ceph:    { mode: 'external', public_network: '', pools: ['rbd', 'cephfs'] },
  addons:  { ingress: 'ingress-nginx', cert_manager: true, monitoring: 'kube-prometheus-stack', gitops: 'none' },
  content: { ref: 'v0.1.0' }
};

export const wizardStore = writable<WizardState>({
  step: 0,
  runId: null,
  contentRef: 'v0.1.0',
  contentDir: null,
  inventory: defaultInventory,
  errors: []
});

export function gotoStep(idx: number) {
  wizardStore.update((s) => ({ ...s, step: Math.max(0, Math.min(6, idx)) }));
}
