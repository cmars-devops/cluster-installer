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
	Name       string    `yaml:"name" json:"name"`
	Domain     string    `yaml:"domain" json:"domain"`
	Kubernetes K8sSpec   `yaml:"kubernetes" json:"kubernetes"`
}

type K8sSpec struct {
	Distro  string `yaml:"distro" json:"distro"`   // rke2 | k3s
	Version string `yaml:"version" json:"version"`
	CNI     string `yaml:"cni" json:"cni"`
}

type NetworkSpec struct {
	PodCIDR     string `yaml:"pod_cidr" json:"pod_cidr"`
	ServiceCIDR string `yaml:"service_cidr" json:"service_cidr"`
	VIP         string `yaml:"vip" json:"vip"`
	LBPool      string `yaml:"lb_pool" json:"lb_pool"`
	Gateway     string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	DNS         string `yaml:"dns,omitempty" json:"dns,omitempty"`
	PrefixLen   int    `yaml:"prefix_len,omitempty" json:"prefix_len,omitempty"`
}

type TargetSpec struct {
	Type        string `yaml:"type" json:"type"` // libvirt | proxmox
	Endpoint    string `yaml:"endpoint" json:"endpoint"`
	SSHKey      string `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
	Username    string `yaml:"username,omitempty" json:"username,omitempty"`
	APIToken    string `yaml:"api_token,omitempty" json:"api_token,omitempty"`
	TLSInsecure bool   `yaml:"tls_insecure,omitempty" json:"tls_insecure,omitempty"`
}

type NodeSpec struct {
	Hostname        string   `yaml:"hostname" json:"hostname"`
	IP              string   `yaml:"ip" json:"ip"`
	Roles           []string `yaml:"roles" json:"roles"`
	OS              string   `yaml:"os" json:"os"` // microos | leap | tumbleweed
	CPU             int      `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	MemoryGB        int      `yaml:"memory_gb,omitempty" json:"memory_gb,omitempty"`
	DiskGB          int      `yaml:"disk_gb,omitempty" json:"disk_gb,omitempty"`
	StorageDevices  []string `yaml:"storage_devices,omitempty" json:"storage_devices,omitempty"`
	NetworkIface    string   `yaml:"network_interface,omitempty" json:"network_interface,omitempty"`
	SSHAuthKeys     []string `yaml:"ssh_authorized_keys,omitempty" json:"ssh_authorized_keys,omitempty"`
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

type CephSpec struct {
	Mode           string   `yaml:"mode" json:"mode"` // external | rook
	PublicNetwork  string   `yaml:"public_network" json:"public_network"`
	ClusterNetwork string   `yaml:"cluster_network,omitempty" json:"cluster_network,omitempty"`
	Pools          []string `yaml:"pools" json:"pools"`
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
