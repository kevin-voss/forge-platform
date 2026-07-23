package azure

// Tag keys applied to every Forge-managed Azure resource.
const (
	TagManaged  = "forge.managed"
	TagOpID     = "forge.op_id"
	TagNodePool = "forge.nodepool"
	TagRole     = "forge.role"

	TagManagedValue = "true"
	RoleNetwork     = "network"
	RoleVolume      = "volume"
	RolePublicIP    = "public_ip"
	RoleVM          = "vm"
)

// ManagedTags returns the standard create tags for a VM.
func ManagedTags(pool, opID string) map[string]string {
	return map[string]string{
		TagManaged:  TagManagedValue,
		TagOpID:     opID,
		TagNodePool: pool,
		TagRole:     RoleVM,
	}
}

// TagFilterManaged returns filters for forge-managed resources.
func TagFilterManaged() map[string]string {
	return map[string]string{TagManaged: TagManagedValue}
}

// TagFilterOpID returns filters for an operation id.
func TagFilterOpID(opID string) map[string]string {
	return map[string]string{
		TagManaged: TagManagedValue,
		TagOpID:    opID,
	}
}

// TagFilterPool returns filters for a node pool's managed resources.
func TagFilterPool(pool string) map[string]string {
	return map[string]string{
		TagManaged:  TagManagedValue,
		TagNodePool: pool,
	}
}
