package inventory

// Inventory mirrors content/schema/inventory.schema.json. Keep these in sync;
// the validator is the source of truth.
type Inventory struct {
	Cluster ClusterSpec   `yaml:"cluster" json:"cluster"`
	Network NetworkSpec   `yaml:"network" json:"network"`
	Target  TargetSpec    `yaml:"target"  json:"target"`
	Nodes   []NodeSpec    `yaml:"nodes"   json:"nodes"`
	Ceph    CephSpec      `yaml:"ceph"    json:"ceph"`
	Addons  AddonsSpec    `yaml:"addons"  json:"addons"`
	Content ContentSpec   `yaml:"content" json:"content"`
}

type ClusterSpec struct {
	Name       string  `yaml:"name" json:"name"`
	Domain     string  `yaml:"domain" json:"domain"`
	Timezone   string  `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Kubernetes K8sSpec `yaml:"kubernetes" json:"kubernetes"`
}

type K8sSpec struct {
	Distro  string `yaml:"distro" json:"distro"`   // rke2 | k3s
	Version string `yaml:"version" json:"version"`
	CNI     string `yaml:"cni" json:"cni"`
}

type NetworkSpec struct {
	PodCIDR          string   `yaml:"pod_cidr" json:"pod_cidr"`
	ServiceCIDR      string   `yaml:"service_cidr" json:"service_cidr"`
	VIP              string   `yaml:"vip" json:"vip"`
	LBPool           string   `yaml:"lb_pool" json:"lb_pool"`
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

type NodeSpec struct {
	Hostname       string   `yaml:"hostname" json:"hostname"`
	IP             string   `yaml:"ip" json:"ip"`
	ClusterIP      string   `yaml:"cluster_ip,omitempty" json:"cluster_ip,omitempty"` // Ceph cluster network (C-Net)
	Roles          []string `yaml:"roles" json:"roles"`
	OS             string   `yaml:"os" json:"os"` // microos | leap | tumbleweed
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
