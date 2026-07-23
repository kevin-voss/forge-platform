package docker

// Label keys applied to every Forge-managed Docker resource.
const (
	LabelManaged  = "forge.managed"
	LabelPool     = "forge.pool"
	LabelOpID     = "forge.op_id"
	LabelNodePool = "forge.nodepool"
	LabelSizeGiB  = "forge.size_gib"

	LabelManagedValue = "true"
)

// ManagedLabels returns the standard create labels for a node container.
func ManagedLabels(pool, opID string) map[string]string {
	return map[string]string{
		LabelManaged:  LabelManagedValue,
		LabelPool:     pool,
		LabelNodePool: pool,
		LabelOpID:     opID,
	}
}
