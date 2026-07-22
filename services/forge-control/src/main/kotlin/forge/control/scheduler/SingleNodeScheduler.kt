package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest

/**
 * Trivial strategy: place every replica on the sole registered node.
 * Real multi-node strategies arrive in 08.03+.
 */
class SingleNodeScheduler(
    private val availableNodes: () -> List<String>,
) : Scheduler {
    constructor(nodeId: String?) : this({
        listOfNotNull(nodeId?.trim()?.takeIf { it.isNotEmpty() })
    })

    override fun place(request: PlacementRequest): PlacementDecision {
        val nodes = availableNodes().map { it.trim() }.filter { it.isNotEmpty() }.distinct()
        if (nodes.isEmpty()) {
            return PlacementDecision.NoNodeAvailable(reason = "no node available")
        }
        val nodeId = nodes.first()
        return PlacementDecision.Assigned(
            nodeId = nodeId,
            strategy = STRATEGY,
            reason = if (nodes.size == 1) "only node available" else "single-node strategy uses first registered node",
        )
    }

    companion object {
        const val STRATEGY: String = "single-node"
    }
}
