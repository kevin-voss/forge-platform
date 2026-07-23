package hetzner

// Label keys applied to every Forge-managed Hetzner resource.
const (
	LabelManaged  = "forge.managed"
	LabelOpID     = "forge.op_id"
	LabelNodePool = "forge.nodepool"
	LabelRole     = "forge.role"

	LabelManagedValue = "true"
	RoleNetwork       = "network"
	RoleVolume        = "volume"
	RoleFloatingIP    = "floating_ip"
	RoleServer        = "server"
)

// ManagedLabels returns the standard create labels for a node server.
func ManagedLabels(pool, opID string) map[string]string {
	return map[string]string{
		LabelManaged:  LabelManagedValue,
		LabelOpID:     opID,
		LabelNodePool: pool,
		LabelRole:     RoleServer,
	}
}

// LabelSelectorManaged returns the Hetzner label_selector for forge-managed resources.
func LabelSelectorManaged() string {
	return LabelManaged + "=" + LabelManagedValue
}

// LabelSelectorOpID returns the label_selector for an operation id.
func LabelSelectorOpID(opID string) string {
	return LabelManaged + "=" + LabelManagedValue + "," + LabelOpID + "==" + opID
}

// LabelSelectorPool returns the label_selector for a node pool's managed resources.
func LabelSelectorPool(pool string) string {
	return LabelManaged + "=" + LabelManagedValue + "," + LabelNodePool + "==" + pool
}
