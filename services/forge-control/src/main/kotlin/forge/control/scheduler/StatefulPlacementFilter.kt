package forge.control.scheduler

import forge.control.scheduler.model.StatefulRole
import forge.control.scheduler.model.StatefulSpec
import forge.control.scheduler.model.UnschedulableReasonCode
import forge.control.scheduler.model.UnschedulableReasonEntry
import forge.control.logging.JsonLog

data class StatefulFilterResult(
    val candidates: List<FleetNode>,
    val eliminated: List<UnschedulableReasonEntry>,
)

/**
 * Hard constraints for stateful workloads: pinned node, volume locality,
 * and primary/replica anti-affinity (primaries never co-locate).
 */
class StatefulPlacementFilter(
    private val volumeLocality: VolumeLocalityStore = InMemoryVolumeLocalityStore(),
    private val placedReplicas: () -> List<Placement> = { emptyList() },
    private val log: JsonLog? = null,
) {
    fun filter(
        candidates: List<FleetNode>,
        deploymentId: String,
        serviceId: String?,
        stateful: StatefulSpec?,
    ): StatefulFilterResult {
        if (stateful == null || stateful.isEmpty()) {
            return StatefulFilterResult(candidates, emptyList())
        }
        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        var remaining = candidates

        val pinned = stateful.resolvedPinnedNodeId()
        if (!pinned.isNullOrBlank()) {
            val kept = remaining.filter { it.id == pinned }
            remaining.filter { it.id != pinned }.forEach { node ->
                eliminated += UnschedulableReasonEntry(
                    nodeId = node.id,
                    reason = UnschedulableReasonCode.StatefulConstraintUnsatisfiable.wire(),
                    detail = "pinned to node $pinned",
                )
            }
            remaining = kept
        }

        val volumeRef = stateful.resolvedVolumeRef()
        if (!volumeRef.isNullOrBlank()) {
            val localNode = volumeLocality.get(volumeRef)
            if (!localNode.isNullOrBlank()) {
                val kept = remaining.filter { it.id == localNode }
                remaining.filter { it.id != localNode }.forEach { node ->
                    eliminated += UnschedulableReasonEntry(
                        nodeId = node.id,
                        reason = UnschedulableReasonCode.StatefulConstraintUnsatisfiable.wire(),
                        detail = "volume $volumeRef local to $localNode",
                    )
                }
                remaining = kept
            }
        }

        val role = stateful.resolvedRole()
        if (role == StatefulRole.Primary || role == StatefulRole.Replica) {
            val placed = placedReplicas()
            val peerPrimaries = placed.filter { p ->
                p.status == PendingQueue.STATUS_PLACED &&
                    p.stateful?.resolvedRole() == StatefulRole.Primary &&
                    (
                        (!serviceId.isNullOrBlank() && p.serviceId == serviceId) ||
                            p.deploymentId.toString() == deploymentId ||
                            (
                                !volumeRef.isNullOrBlank() &&
                                    p.stateful?.resolvedVolumeRef() == volumeRef
                                )
                        )
            }
            if (role == StatefulRole.Primary) {
                val occupied = peerPrimaries.mapNotNull { it.nodeId }.toSet()
                val kept = remaining.filter { it.id !in occupied }
                remaining.filter { it.id in occupied }.forEach { node ->
                    eliminated += UnschedulableReasonEntry(
                        nodeId = node.id,
                        reason = UnschedulableReasonCode.StatefulConstraintUnsatisfiable.wire(),
                        detail = "primary anti-affinity: node hosts another primary",
                    )
                }
                remaining = kept
            }
        }

        return StatefulFilterResult(remaining, eliminated)
    }

    /** Soft score bonus for volume-local nodes when locality is not yet recorded. */
    fun score(node: FleetNode, stateful: StatefulSpec?): Double {
        if (stateful == null || stateful.isEmpty()) return 0.0
        val volumeRef = stateful.resolvedVolumeRef() ?: return 0.0
        val local = volumeLocality.get(volumeRef)
        return when {
            local == null -> 0.0
            local == node.id -> VOLUME_LOCAL_BONUS
            else -> 0.0
        }
    }

    fun recordPlacement(
        deploymentId: String,
        volumeRef: String?,
        selectedNode: String,
        reason: String,
    ) {
        if (!volumeRef.isNullOrBlank()) {
            volumeLocality.put(volumeRef, selectedNode)
        }
        log?.info(
            "stateful placement",
            "event" to "stateful_placement",
            "deployment" to deploymentId,
            "volume" to (volumeRef ?: ""),
            "selected_node" to selectedNode,
            "reason" to reason,
        )
    }

    companion object {
        const val VOLUME_LOCAL_BONUS: Double = 100.0
        const val REASON: String = "stateful constraint unsatisfiable"

        fun noop(): StatefulPlacementFilter = StatefulPlacementFilter()
    }
}
