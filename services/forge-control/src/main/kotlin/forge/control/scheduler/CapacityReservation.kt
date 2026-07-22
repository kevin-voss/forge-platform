package forge.control.scheduler

import forge.control.scheduler.model.ResourceRequirements

/**
 * Atomically reserve / release node capacity so concurrent placements cannot
 * double-book a node. Backed by [NodeStore] CAS updates on `allocation_json`.
 */
class CapacityReservation(
    private val nodes: NodeStore,
) {
    fun tryReserve(nodeId: String, requirements: ResourceRequirements): Boolean =
        nodes.tryReserve(nodeId, requirements)

    fun release(nodeId: String, requirements: ResourceRequirements): Boolean =
        nodes.release(nodeId, requirements)

    fun releaseSlots(nodeId: String, slots: Int): Boolean =
        release(nodeId, ResourceRequirements(slots = slots))
}
