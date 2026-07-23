package provider

// Region is a provider region/location.
type Region struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MachineType describes a provider machine size.
type MachineType struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CPU       int    `json:"cpu"`
	MemoryMiB int    `json:"memoryMiB"`
	DiskGiB   int    `json:"diskGiB,omitempty"`
	GPU       int    `json:"gpu,omitempty"`
	Region    string `json:"region,omitempty"`
}

// ProviderNode is a machine as seen by a cloud/local provider.
type ProviderNode struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Region      string            `json:"region,omitempty"`
	MachineType string            `json:"machineType,omitempty"`
	Address     string            `json:"address,omitempty"`
	Phase       string            `json:"phase,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Network is a provider network.
type Network struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Region string `json:"region,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// Disk is an attached or attachable volume.
type Disk struct {
	ID       string `json:"id"`
	NodeID   string `json:"nodeId,omitempty"`
	SizeGiB  int    `json:"sizeGiB"`
	Attached bool   `json:"attached"`
}

// PublicIP is a provider public address binding.
type PublicIP struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	NodeID  string `json:"nodeId,omitempty"`
}

// Pricing is a unit cost snapshot for a machine type in a region.
type Pricing struct {
	Region      string  `json:"region"`
	MachineType string  `json:"machineType"`
	HourlyUSD   float64 `json:"hourlyUSD"`
	Currency    string  `json:"currency,omitempty"`
}

// CreateNetworkRequest creates a provider network.
type CreateNetworkRequest struct {
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// CreateNodeRequest creates a provider node/machine.
type CreateNodeRequest struct {
	Name        string            `json:"name"`
	Region      string            `json:"region"`
	MachineType string            `json:"machineType"`
	DiskGiB     int               `json:"diskGiB,omitempty"`
	PublicIP    bool              `json:"publicIP,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	NodePool    string            `json:"nodePool,omitempty"`
	Slot        int               `json:"slot,omitempty"`
	// BootstrapToken is a single-use epic-22 token; never log unmasked.
	BootstrapToken string `json:"bootstrapToken,omitempty"`
	// UserData is optional cloud-init / install script rendered by bootstrap.Payload.
	UserData string `json:"userData,omitempty"`
	// Env is optional provider-agnostic env injected at create (e.g. docker).
	Env map[string]string `json:"env,omitempty"`
}

// AttachDiskRequest attaches a disk to a node.
type AttachDiskRequest struct {
	SizeGiB int    `json:"sizeGiB"`
	Name    string `json:"name,omitempty"`
}

// CreatePublicIPRequest allocates a public IP.
type CreatePublicIPRequest struct {
	Region string `json:"region,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	Name   string `json:"name,omitempty"`
}
