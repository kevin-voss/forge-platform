package forge.control.scheduler

import forge.control.scheduler.model.ResourceRequirements
import forge.control.scheduler.model.UnschedulableReasonCode
import forge.control.scheduler.model.UnschedulableReasonEntry
import forge.control.scheduler.model.UnschedulableReasons

/** Shared free-capacity checks for placement strategies. */
internal object PlacementCapacity {
    fun freeSlots(node: FleetNode): Int {
        val total = node.allocatable?.slots ?: node.capacity.slots
        return (total - node.allocation.slots).coerceAtLeast(0)
    }

    fun freeCpuMillis(node: FleetNode): Int? {
        val total = node.allocatable?.cpuMillis ?: node.capacity.cpuMillis ?: return null
        val used = node.allocation.cpuMillis ?: 0
        return (total - used).coerceAtLeast(0)
    }

    fun freeMemMb(node: FleetNode): Int? {
        val total = node.allocatable?.memMb ?: node.capacity.memMb ?: return null
        val used = node.allocation.memMb ?: 0
        return (total - used).coerceAtLeast(0)
    }

    fun freeDiskMb(node: FleetNode): Int? {
        val total = node.allocatable?.diskMb ?: node.capacity.diskMb ?: return null
        val used = node.allocation.diskMb ?: 0
        return (total - used).coerceAtLeast(0)
    }

    fun fits(node: FleetNode, requirements: ResourceRequirements): Boolean =
        evaluate(node, requirements).ok

    fun evaluate(
        node: FleetNode,
        requirements: ResourceRequirements,
    ): CapacityEval {
        if (node.status != "online") {
            return CapacityEval(
                ok = false,
                reason = UnschedulableReasons.entry(
                    nodeId = node.id,
                    code = UnschedulableReasonCode.InsufficientSlots,
                    requested = requirements.slots,
                    free = 0,
                ),
            )
        }

        val resolved = RequirementsResolver.resolve(requirements)
        val requestsAuth = resolved.requestsAuthoritative

        if (!requestsAuth) {
            // Epic-08 compatibility: slots-only filtering; derived requests are informational.
            val freeSlots = freeSlots(node)
            if (freeSlots < resolved.slots) {
                return CapacityEval(
                    ok = false,
                    reason = UnschedulableReasons.entry(
                        nodeId = node.id,
                        code = UnschedulableReasonCode.InsufficientSlots,
                        requested = resolved.slots,
                        free = freeSlots,
                    ),
                )
            }
            return CapacityEval(ok = true)
        }

        // Requests authoritative: slots ignored for filtering; each requested
        // dimension must be present on the node.
        cpuCheck(node, resolved.cpuMillis, required = true)?.let { return it }
        memCheck(node, resolved.memoryMb, required = true)?.let { return it }
        diskCheck(node, resolved.diskMb, required = true)?.let { return it }
        return CapacityEval(ok = true)
    }

    fun candidates(
        nodes: NodeStore,
        requirements: ResourceRequirements,
        excluded: Set<String> = emptySet(),
    ): List<FleetNode> =
        nodes.list()
            .filter { it.id !in excluded && fits(it, requirements) }
            .sortedBy { it.id }

    fun eliminated(
        nodes: NodeStore,
        requirements: ResourceRequirements,
        excluded: Set<String> = emptySet(),
    ): List<UnschedulableReasonEntry> =
        nodes.list()
            .filter { it.id !in excluded }
            .mapNotNull { node ->
                val eval = evaluate(node, requirements)
                if (eval.ok) null else eval.reason
            }

    private fun cpuCheck(node: FleetNode, need: Int?, required: Boolean): CapacityEval? {
        if (need == null) return null
        val free = freeCpuMillis(node)
        if (free == null) {
            return if (required) {
                CapacityEval(
                    ok = false,
                    reason = UnschedulableReasons.entry(
                        nodeId = node.id,
                        code = UnschedulableReasonCode.InsufficientCPU,
                        requested = need,
                        free = 0,
                    ),
                )
            } else {
                null
            }
        }
        if (free < need) {
            return CapacityEval(
                ok = false,
                reason = UnschedulableReasons.entry(
                    nodeId = node.id,
                    code = UnschedulableReasonCode.InsufficientCPU,
                    requested = need,
                    free = free,
                ),
            )
        }
        return null
    }

    private fun memCheck(node: FleetNode, need: Int?, required: Boolean): CapacityEval? {
        if (need == null) return null
        val free = freeMemMb(node)
        if (free == null) {
            return if (required) {
                CapacityEval(
                    ok = false,
                    reason = UnschedulableReasons.entry(
                        nodeId = node.id,
                        code = UnschedulableReasonCode.InsufficientMemory,
                        requested = need,
                        free = 0,
                    ),
                )
            } else {
                null
            }
        }
        if (free < need) {
            return CapacityEval(
                ok = false,
                reason = UnschedulableReasons.entry(
                    nodeId = node.id,
                    code = UnschedulableReasonCode.InsufficientMemory,
                    requested = need,
                    free = free,
                ),
            )
        }
        return null
    }

    private fun diskCheck(node: FleetNode, need: Int?, required: Boolean): CapacityEval? {
        if (need == null) return null
        val free = freeDiskMb(node)
        if (free == null) {
            return if (required) {
                CapacityEval(
                    ok = false,
                    reason = UnschedulableReasons.entry(
                        nodeId = node.id,
                        code = UnschedulableReasonCode.InsufficientDisk,
                        requested = need,
                        free = 0,
                    ),
                )
            } else {
                null
            }
        }
        if (free < need) {
            return CapacityEval(
                ok = false,
                reason = UnschedulableReasons.entry(
                    nodeId = node.id,
                    code = UnschedulableReasonCode.InsufficientDisk,
                    requested = need,
                    free = free,
                ),
            )
        }
        return null
    }
}

internal data class CapacityEval(
    val ok: Boolean,
    val reason: UnschedulableReasonEntry? = null,
)
