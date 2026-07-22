package forge.control.scheduler

import forge.control.scheduler.model.ResourceRequirements

/** Shared free-capacity checks for placement strategies. */
internal object PlacementCapacity {
    fun freeSlots(node: FleetNode): Int =
        (node.capacity.slots - node.allocation.slots).coerceAtLeast(0)

    fun freeCpuMillis(node: FleetNode): Int? {
        val total = node.capacity.cpuMillis ?: return null
        val used = node.allocation.cpuMillis ?: 0
        return (total - used).coerceAtLeast(0)
    }

    fun freeMemMb(node: FleetNode): Int? {
        val total = node.capacity.memMb ?: return null
        val used = node.allocation.memMb ?: 0
        return (total - used).coerceAtLeast(0)
    }

    fun fits(node: FleetNode, requirements: ResourceRequirements): Boolean {
        if (node.status != "online") return false
        if (freeSlots(node) < requirements.slots) return false
        val needCpu = requirements.cpuMillis
        if (needCpu != null) {
            val free = freeCpuMillis(node) ?: return false
            if (free < needCpu) return false
        }
        val needMem = requirements.memMb
        if (needMem != null) {
            val free = freeMemMb(node) ?: return false
            if (free < needMem) return false
        }
        return true
    }

    fun candidates(
        nodes: NodeStore,
        requirements: ResourceRequirements,
        excluded: Set<String> = emptySet(),
    ): List<FleetNode> =
        nodes.list()
            .filter { it.id !in excluded && fits(it, requirements) }
            .sortedBy { it.id }
}
