package inventory

import "fmt"

// Inventory mirrors content/schema/inventory.schema.json. Keep these in sync;
// the validator is the source of truth.
type Inventory struct {
	Cluster     ClusterSpec     `yaml:"cluster" json:"cluster"`
	Network     NetworkSpec     `yaml:"network" json:"network"`
	Target      TargetSpec      `yaml:"target"  json:"target"`
	Nodes       []NodeSpec      `yaml:"nodes"   json:"nodes"`
	Ceph        CephSpec        `yaml:"ceph"    json:"ceph"`
	Addons      AddonsSpec      `yaml:"addons"  json:"addons"`
	Content     ContentSpec     `yaml:"content" json:"content"`
	ClusterAuth ClusterAuthSpec `yaml:"cluster_auth,omitempty" json:"cluster_auth,omitempty"`
}

// ClusterAuthSpec carries cluster-wide credentials baked into autoinstall
// user-data (so freshly-installed nodes are reachable via SSH without
// further intervention). Per-node NodeSpec.SSHAuthKeys override
// SSHAuthorizedKeys when set; SSHImportGitHub is always cluster-wide.
type ClusterAuthSpec struct {
	// Username is the sudo account autoinstall creates on every node.
	// SSH keys, the optional console password, and the sudoers NOPASSWD
	// entry all attach to THIS account. The wizard defaults to
	// "triangles" for back-compat with the original cluster flow; an
	// operator-friendly value is whatever they're used to ("ubuntu",
	// "admin", their own login name, etc.). Empty = use default.
	Username          string   `yaml:"username,omitempty"             json:"username,omitempty"`
	// GitHub usernames whose keys are fetched from github.com/<name>.keys
	// at first boot via ssh-import-id-gh. Lower friction than pasting raw
	// keys and stays current as the operator rotates their GitHub keys.
	SSHImportGitHub   []string `yaml:"ssh_import_github,omitempty"    json:"ssh_import_github,omitempty"`
	// Raw SSH public keys (ssh-ed25519 / ssh-rsa lines). Optional fallback
	// when GitHub-import isn't available (offline lab, internal-only nodes).
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty" json:"ssh_authorized_keys,omitempty"`
	// NodePassword is plain-text — applied to NodeSpec.OS users via
	// chpasswd in late-commands (subiquity's identity.password requires
	// SHA-512 crypt which the wizard doesn't currently produce native-Go).
	// Used for console / sudo prompts only; SSH still goes through keys.
	NodePassword      string   `yaml:"node_password,omitempty"        json:"node_password,omitempty"`
}

// SudoUser returns the configured sudo account name, falling back to
// the historical default "triangles" when the operator hasn't picked
// one. Centralised so every consumer (template substitution, SSH
// dial-in, verify, hosts.yml) agrees on one value.
func (a ClusterAuthSpec) SudoUser() string {
	if a.Username != "" {
		return a.Username
	}
	return "triangles"
}

type ClusterSpec struct {
	Name     string `yaml:"name" json:"name"`
	Domain   string `yaml:"domain" json:"domain"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	// Topology controls which pipeline stages run.
	//   ceph-only | k8s-only | combined → cluster pipelines (multi-node)
	//   dev-vm                          → single-VM unattended-install verification harness
	Topology     string            `yaml:"topology,omitempty" json:"topology,omitempty"`
	Kubernetes   K8sSpec           `yaml:"kubernetes" json:"kubernetes"`
	ExternalCeph *ExternalCephSpec `yaml:"external_ceph,omitempty" json:"external_ceph,omitempty"`
}

// TopologyDevVM is the topology value reserved for the single-VM
// unattended-install verification mode. The orchestrator skips every
// cluster-level stage (preflight, ceph, k8s, csi, addons) and runs a
// dedicated verify stage at the end. Used by the wizard's "신규 VM 생성
// (Dev VM)" flow and as a smoke-test harness for the OS-install plumbing
// that the cluster pipeline depends on.
const TopologyDevVM = "dev-vm"

// IsDevVM reports whether this inventory is running in single-VM mode.
func (c ClusterSpec) IsDevVM() bool { return c.Topology == TopologyDevVM }

type K8sSpec struct {
	Distro  string `yaml:"distro" json:"distro"`   // rke2 | k3s
	Version string `yaml:"version" json:"version"`
	CNI     string `yaml:"cni" json:"cni"`

	// Token is the cluster join token. Generated once per cluster — same
	// token is used by additional servers/agents to join later. Secret.
	Token string `yaml:"token,omitempty" json:"token,omitempty"`

	// KubeVIPInterface is the NIC kube-vip ARP-advertises the API VIP on.
	// ESXi vmxnet3: ens192; libvirt virtio: eth0/enp1s0.
	KubeVIPInterface string `yaml:"kube_vip_interface,omitempty" json:"kube_vip_interface,omitempty"`

	// TLSSANs are extra SANs for the API server cert. VIP and node IPs
	// are added automatically; this is for external DNS like
	// "k8s-prod.triangles.com".
	TLSSANs []string `yaml:"tls_sans,omitempty" json:"tls_sans,omitempty"`
}

// ExternalCephSpec captures connection info for a pre-existing Ceph
// cluster the wizard should NOT bootstrap, but should wire ceph-csi
// against. Used when topology=k8s-only and the operator wants
// RBD/CephFS PVCs backed by an independently-managed Ceph cluster.
type ExternalCephSpec struct {
	MonEndpoints []string `yaml:"mon_endpoints" json:"mon_endpoints"`
	FSID         string   `yaml:"fsid"          json:"fsid"`
	ClientUser   string   `yaml:"client_user"   json:"client_user"` // narrow scope user, e.g. "k8s-rbd"
	ClientKey    string   `yaml:"client_key"    json:"client_key"`  // raw or base64 keyring secret
	Pool         string   `yaml:"pool"          json:"pool"`        // e.g. "rbd-pool"
}

type NetworkSpec struct {
	PodCIDR          string   `yaml:"pod_cidr" json:"pod_cidr"`
	ServiceCIDR      string   `yaml:"service_cidr" json:"service_cidr"`
	VIP              string   `yaml:"vip" json:"vip"`
	LBPool           string   `yaml:"lb_pool" json:"lb_pool"`
	IngressLBIP      string   `yaml:"ingress_lb_ip,omitempty" json:"ingress_lb_ip,omitempty"`
	Gateway          string   `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	Nameservers      []string `yaml:"nameservers,omitempty" json:"nameservers,omitempty"`
	PrefixLen        int      `yaml:"prefix_len,omitempty" json:"prefix_len,omitempty"`
	ClusterPrefixLen int      `yaml:"cluster_prefix_len,omitempty" json:"cluster_prefix_len,omitempty"`
}

type TargetSpec struct {
	Type     string `yaml:"type" json:"type"` // libvirt | proxmox | esxi
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"` // default "root"

	// libvirt: required path to SSH private key.
	// esxi:    optional alternative to Password.
	SSHKey string `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`

	// proxmox: required, format "user@pam!tokenid=secret".
	APIToken string `yaml:"api_token,omitempty" json:"api_token,omitempty"`

	// esxi: vSphere API + SSH share a single root password by ESXi convention.
	// Never persist in plaintext outside the per-user %LOCALAPPDATA% tree.
	Password string `yaml:"password,omitempty" json:"password,omitempty"`

	// ESXi-specific placement:
	Datastore    string `yaml:"datastore,omitempty" json:"datastore,omitempty"`
	ISODatastore string `yaml:"iso_datastore,omitempty" json:"iso_datastore,omitempty"`
	Network      string `yaml:"network,omitempty" json:"network,omitempty"`

	TLSInsecure bool   `yaml:"tls_insecure,omitempty" json:"tls_insecure,omitempty"`
	AdvertiseIP string `yaml:"advertise_ip,omitempty" json:"advertise_ip,omitempty"`
}

// DiskSpec describes one virtual disk attached to a VM. The first disk
// in NodeSpec.Disks is the OS install disk (where the autoinstall
// formats and lays down /). Additional entries are extra blank
// volumes the guest OS sees as /dev/sd[bcd...] etc.
type DiskSpec struct {
	SizeGB       int    `yaml:"size_gb" json:"size_gb"`
	// Datastore overrides the VM-level datastore for THIS disk only.
	// Empty = use the VM's primary datastore (Step 4's datastore
	// picker). Common pattern: keep the OS disk on a fast SSD array
	// and put bulk data disks on a larger/cheaper one.
	Datastore    string `yaml:"datastore,omitempty"     json:"datastore,omitempty"`
	// Provisioning overrides VM-level provisioning for THIS disk only.
	// Same enum as NodeSpec.DiskProvisioning: thin / thick / thick-eager.
	Provisioning string `yaml:"provisioning,omitempty"  json:"provisioning,omitempty"`
	// Label is a free-form name shown in vSphere's hardware list. When
	// empty the module picks "disk0", "disk1", … in order.
	Label        string `yaml:"label,omitempty"         json:"label,omitempty"`
}

// NICSpec describes one virtual NIC attached to a VM. The first entry
// is the primary — wait_ssh + verify dial its IP, late-commands run
// against it, and it carries the default route by convention. Extra
// entries get configured in netplan but the wizard doesn't touch
// their routing rules — that's a per-deployment decision the operator
// makes inside the guest.
type NICSpec struct {
	// Network is the ESXi port-group name (vSwitch port group). On
	// libvirt this maps to the network name; on Proxmox to the bridge.
	Network     string   `yaml:"network"                json:"network"`
	// IPMode: "static" (default) or "dhcp". Per-NIC.
	IPMode      string   `yaml:"ip_mode,omitempty"      json:"ip_mode,omitempty"`
	// IP / PrefixLen / Gateway / Nameservers apply when IPMode=static.
	IP          string   `yaml:"ip,omitempty"           json:"ip,omitempty"`
	PrefixLen   int      `yaml:"prefix_len,omitempty"   json:"prefix_len,omitempty"`
	Gateway     string   `yaml:"gateway,omitempty"      json:"gateway,omitempty"`
	Nameservers []string `yaml:"nameservers,omitempty"  json:"nameservers,omitempty"`
	// MAC is allocated by the orchestrator before TF apply via a
	// deterministic hash of (cluster_name, hostname, nic_index) —
	// makes redeploys reuse the same MAC, which keeps DHCP leases
	// stable and lets the autoinstall match on it.
	MAC         string   `yaml:"mac,omitempty"          json:"mac,omitempty"`
	// Label is a free-form name. When empty the module picks
	// "nic0", "nic1", … in order.
	Label       string   `yaml:"label,omitempty"        json:"label,omitempty"`
}

type NodeSpec struct {
	Hostname       string   `yaml:"hostname" json:"hostname"`
	// DisplayName is the label vSphere shows in its inventory tree
	// (the "VM 이름" column). When empty, Hostname is reused — most
	// users want them the same. When set, vSphere renders this value
	// while the guest OS still reports Hostname (some operators want
	// "DEV-DEVVM-01 (cmars)" in vSphere but plain "devvm-01" inside
	// the OS).
	DisplayName    string   `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	// Disks lists every virtual disk to attach, OS install disk first.
	// When empty, the legacy single-disk fields (DiskGB +
	// Datastore/DiskProvisioning + the cluster Ceph helpers
	// data_devices/db_devices/wal_devices) are translated to a Disks
	// list at tfvars-render time. dev-vm UI sets this directly so
	// each disk can have its own datastore + provisioning.
	Disks          []DiskSpec `yaml:"disks,omitempty" json:"disks,omitempty"`
	// NICs lists every virtual NIC to attach, primary first. When
	// empty, a single NIC is synthesised from target.Network +
	// per-node IP/PrimaryMAC at tfvars-render time. dev-vm UI sets
	// this directly when the operator wants more than one NIC.
	NICs           []NICSpec `yaml:"nics,omitempty"  json:"nics,omitempty"`
	IP             string   `yaml:"ip" json:"ip"`
	ClusterIP      string   `yaml:"cluster_ip,omitempty" json:"cluster_ip,omitempty"` // Ceph cluster network (C-Net)
	Roles          []string `yaml:"roles" json:"roles"`
	OS             string   `yaml:"os" json:"os"` // microos | leap | tumbleweed | ubuntu
	OSVersion      string   `yaml:"os_version,omitempty" json:"os_version,omitempty"` // optional pin, e.g. "26.04"
	// IPMode selects how the VM gets its primary-NIC IP at install time.
	//   "" / "static"  → use NodeSpec.IP, NetworkSpec.Gateway, etc.
	//   "dhcp"         → autoinstall configures dhcp4 on the primary NIC,
	//                    NodeSpec.IP/Gateway are ignored. Wait_ssh and
	//                    verify still need a discoverable IP — for now the
	//                    operator should pin a static DHCP lease so the
	//                    NodeSpec.IP entry stays accurate.
	IPMode         string   `yaml:"ip_mode,omitempty" json:"ip_mode,omitempty"`
	CPU            int      `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	MemoryGB       int      `yaml:"memory_gb,omitempty" json:"memory_gb,omitempty"`
	DiskGB         int      `yaml:"disk_gb,omitempty" json:"disk_gb,omitempty"`
	// OSD device layout — only meaningful when roles includes "ceph-osd".
	// Mirrors cephadm's OSD service spec (data_devices.paths /
	// db_devices.paths / wal_devices.paths).
	DataDevices    []string `yaml:"data_devices,omitempty"    json:"data_devices,omitempty"`
	DBDevices      []string `yaml:"db_devices,omitempty"      json:"db_devices,omitempty"`
	WALDevices     []string `yaml:"wal_devices,omitempty"     json:"wal_devices,omitempty"`
	OSDsPerDevice  int      `yaml:"osds_per_device,omitempty" json:"osds_per_device,omitempty"`
	OSDDataSizeGB  int      `yaml:"osd_data_size_gb,omitempty" json:"osd_data_size_gb,omitempty"`
	OSDDBSizeGB    int      `yaml:"osd_db_size_gb,omitempty"   json:"osd_db_size_gb,omitempty"`
	OSDWALSizeGB   int      `yaml:"osd_wal_size_gb,omitempty"  json:"osd_wal_size_gb,omitempty"`
	OSDEncrypted   bool     `yaml:"osd_encrypted,omitempty"   json:"osd_encrypted,omitempty"`
	DeviceClass    string   `yaml:"device_class,omitempty"    json:"device_class,omitempty"` // auto | hdd | ssd | nvme

	// StorageDevices is the legacy field name — kept as a backward-compat
	// alias for DataDevices. New inventories should use DataDevices.
	StorageDevices []string `yaml:"storage_devices,omitempty" json:"storage_devices,omitempty"`
	NetworkIface   string   `yaml:"network_interface,omitempty" json:"network_interface,omitempty"`
	PrimaryMAC     string   `yaml:"primary_mac,omitempty" json:"primary_mac,omitempty"` // discovered post-VM-create
	SSHAuthKeys    []string `yaml:"ssh_authorized_keys,omitempty" json:"ssh_authorized_keys,omitempty"`

	// Datastore overrides target.Datastore for THIS node only. Common pattern:
	// distribute Ceph CORE / OSD nodes across different physical arrays so a
	// single hardware failure can't take quorum down. Blank = inherit cluster.
	Datastore string `yaml:"datastore,omitempty" json:"datastore,omitempty"`

	// DiskProvisioning controls how the VM's virtual disks are allocated:
	//   thin         — sparse / on-demand (default; storage-efficient)
	//   thick        — fully pre-allocated, lazy zeroing
	//   thick-eager  — pre-allocated + zeroed at create (recommended for
	//                  Ceph OSDs to avoid first-write throughput penalty;
	//                  ESXi only — falls back to 'thick' on libvirt/Proxmox)
	DiskProvisioning string `yaml:"disk_provisioning,omitempty" json:"disk_provisioning,omitempty"`
}

// HasRole is a template helper.
// EffectiveDisks returns the canonical disk list for this node. When
// NodeSpec.Disks is set (dev-vm UI populates it directly) it's used
// verbatim. Otherwise the function synthesises a list from the legacy
// single-disk fields (DiskGB + per-node Datastore/DiskProvisioning)
// plus any cluster-mode Ceph extras computed by extraDisksFor() in
// run/tfvars.go. Centralising this here keeps cluster + dev-vm flows
// on one downstream pipeline.
//
// extras is the ordered list of additional disk sizes the cluster
// path computes from data/db/wal devices. It's passed in instead of
// being recomputed here because the Ceph helpers live in the run
// package and we don't want a cyclic import.
func (n NodeSpec) EffectiveDisks(extras []int) []DiskSpec {
	if len(n.Disks) > 0 {
		return n.Disks
	}
	rootSize := n.DiskGB
	if rootSize == 0 {
		rootSize = 40
	}
	out := []DiskSpec{{
		SizeGB:       rootSize,
		Datastore:    n.Datastore,
		Provisioning: n.DiskProvisioning,
		Label:        "disk0",
	}}
	for i, gb := range extras {
		out = append(out, DiskSpec{
			SizeGB:       gb,
			Datastore:    n.Datastore,
			Provisioning: n.DiskProvisioning,
			Label:        fmt.Sprintf("disk%d", i+1),
		})
	}
	return out
}

// EffectiveNICs returns the canonical NIC list. When NodeSpec.NICs is
// set (dev-vm multi-NIC UI) it's used verbatim. Otherwise a single
// primary NIC is synthesised from target.Network + per-node IP /
// IPMode / PrimaryMAC. The cluster path always falls into the
// synthesised case (single NIC per node) which keeps every existing
// inventory backward-compatible.
//
// targetNetwork is target.Network (the ESXi port-group from Step 2)
// — the synthesised NIC inherits this when no per-node override
// exists. Cluster prefix_len + gateway + nameservers come from the
// shared NetworkSpec; the caller wires those in separately when
// rendering netplan, since they live outside NodeSpec.
func (n NodeSpec) EffectiveNICs(targetNetwork string) []NICSpec {
	if len(n.NICs) > 0 {
		return n.NICs
	}
	return []NICSpec{{
		Network: targetNetwork,
		IPMode:  n.IPMode,
		IP:      n.IP,
		MAC:     n.PrimaryMAC,
		Label:   "nic0",
	}}
}

func (n NodeSpec) HasRole(role string) bool {
	for _, r := range n.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsCephOnly reports whether the node carries only ceph-* roles.
func (n NodeSpec) IsCephOnly() bool {
	for _, r := range n.Roles {
		if r != "ceph-mon" && r != "ceph-mgr" && r != "ceph-osd" && r != "ceph-mds" && r != "ceph-rgw" {
			return false
		}
	}
	return len(n.Roles) > 0
}

func (n NodeSpec) NeedsCeph() bool {
	for _, r := range n.Roles {
		if len(r) >= 5 && r[:5] == "ceph-" {
			return true
		}
	}
	return false
}

// NeedsCephOSD reports whether this node will host OSDs (gets data + WAL/DB devices).
func (n NodeSpec) NeedsCephOSD() bool { return n.HasRole("ceph-osd") }

// HasClusterNIC reports whether the node has a second NIC on the Ceph cluster network.
func (n NodeSpec) HasClusterNIC() bool { return n.ClusterIP != "" }

// UsesAutoinstall reports whether this node uses the Ubuntu autoinstall flow
// (Subiquity + cloud-init NoCloud) instead of openSUSE Agama or Combustion.
func (n NodeSpec) UsesAutoinstall() bool { return n.OS == "ubuntu" }

// UsesAgama reports whether this node uses the openSUSE Agama installer
// (Leap 16+ / Tumbleweed). Agama-driven nodes need ISO remaster on ESXi
// and direct kernel boot on libvirt.
func (n NodeSpec) UsesAgama() bool { return n.OS == "leap" || n.OS == "tumbleweed" }

// UsesKernelBoot reports whether the orchestrator must extract vmlinuz/initrd
// from the install ISO and pass them to Terraform for direct kernel boot.
// Currently Agama-only (Leap/Tumbleweed). Ubuntu autoinstall boots from the
// remastered ISO via CD-ROM, MicroOS uses the qcow2 base — neither needs the
// kernel-extraction path.
func (n NodeSpec) UsesKernelBoot() bool { return n.UsesAgama() }

// UsesISOInstall reports whether this node boots from an attached install
// ISO (remastered to embed our autoinstall cmdline) — Ubuntu autoinstall on
// every target, Agama on ESXi. Libvirt Agama uses direct kernel boot
// instead, so this is target-aware at the call site.
func (n NodeSpec) UsesISOInstall() bool { return n.UsesAutoinstall() || n.UsesAgama() }

// NeedsK3sSELinux is true for Leap Micro nodes that will run K3s/RKE2 — those
// must install k3s-selinux on first boot via transactional-update + reboot.
func (n NodeSpec) NeedsK3sSELinux() bool {
	if n.OS != "microos" {
		return false
	}
	for _, r := range n.Roles {
		if r == "control-plane" || r == "worker" || r == "etcd" {
			return true
		}
	}
	return false
}

// StorageDevicesJSON renders a Go-string-quoted JSON array for templates.
func (n NodeSpec) StorageDevicesJSON() string {
	if len(n.StorageDevices) == 0 {
		return "[]"
	}
	out := "["
	for i, d := range n.StorageDevices {
		if i > 0 {
			out += ", "
		}
		out += `"` + d + `"`
	}
	return out + "]"
}

type CephSpec struct {
	Mode           string   `yaml:"mode" json:"mode"` // external | rook
	PublicNetwork  string   `yaml:"public_network" json:"public_network"`
	ClusterNetwork string   `yaml:"cluster_network,omitempty" json:"cluster_network,omitempty"`
	Pools          []string `yaml:"pools" json:"pools"`

	// Replication is the default replica count for RBD/CephFS pools.
	// 3 = standard, 2 = lab-only, 1 = single-node only.
	Replication int `yaml:"replication,omitempty" json:"replication,omitempty"`

	// FailureDomain is the CRUSH bucket type Ceph spreads replicas across.
	// 'host' is the standard choice; 'rack' / 'chassis' require CRUSH
	// topology setup; 'osd' should never be used in production.
	FailureDomain string `yaml:"failure_domain,omitempty" json:"failure_domain,omitempty"`

	// Defaults applied when per-node OSD fields are unset.
	DefaultOSDsPerDevice int  `yaml:"default_osds_per_device,omitempty" json:"default_osds_per_device,omitempty"`
	DefaultEncrypted     bool `yaml:"default_encrypted,omitempty"        json:"default_encrypted,omitempty"`

	// Per-VM virtual-disk sizes for OSDs. Applied uniformly across nodes;
	// the disk COUNT per node equals the number of paths in the matching
	// device list (e.g. data_devices=[/dev/sdb,/dev/sdc] → 2 disks @
	// OSDDataDiskSizeGB each). 0 = don't allocate disks of that purpose.
	OSDDataDiskSizeGB int `yaml:"osd_data_disk_size_gb,omitempty" json:"osd_data_disk_size_gb,omitempty"`
	OSDDBDiskSizeGB   int `yaml:"osd_db_disk_size_gb,omitempty"   json:"osd_db_disk_size_gb,omitempty"`
	OSDWALDiskSizeGB  int `yaml:"osd_wal_disk_size_gb,omitempty"  json:"osd_wal_disk_size_gb,omitempty"`
}

type AddonsSpec struct {
	Ingress     string `yaml:"ingress,omitempty" json:"ingress,omitempty"`
	CertManager bool   `yaml:"cert_manager,omitempty" json:"cert_manager,omitempty"`
	Monitoring  string `yaml:"monitoring,omitempty" json:"monitoring,omitempty"`
	GitOps      string `yaml:"gitops,omitempty" json:"gitops,omitempty"`
}

type ContentSpec struct {
	Ref  string `yaml:"ref" json:"ref"`
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`
}
