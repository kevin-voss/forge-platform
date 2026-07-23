package forge.control.scheduler.model

/**
 * Result of [forge.control.scheduler.Scheduler.place].
 * Typed failure (no crash) when the fleet has no eligible node.
 */
sealed class PlacementDecision {
    data class Assigned(
        val nodeId: String,
        val strategy: String,
        val reason: String,
        val trace: PlacementTrace? = null,
    ) : PlacementDecision() {
        init {
            require(nodeId.isNotBlank()) { "nodeId must not be blank" }
            require(strategy.isNotBlank()) { "strategy must not be blank" }
        }
    }

    data class NoNodeAvailable(
        val reason: String = "no node available",
        val unschedulableReasons: List<UnschedulableReasonEntry> = emptyList(),
        val trace: PlacementTrace? = null,
    ) : PlacementDecision()
}
