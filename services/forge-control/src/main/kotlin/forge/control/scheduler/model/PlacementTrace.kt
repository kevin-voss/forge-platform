package forge.control.scheduler.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class PlacementTraceFilter(
    val name: String,
    val eliminated: List<UnschedulableReasonEntry> = emptyList(),
)

@Serializable
data class PlacementTraceScore(
    @SerialName("node_id") val nodeId: String,
    val score: Double,
    val detail: String? = null,
)

/**
 * Appendable explainability record for a placement decision.
 * Later epic-25 steps append additional filters/scores.
 */
@Serializable
data class PlacementTrace(
    val strategy: String? = null,
    val filters: List<PlacementTraceFilter> = emptyList(),
    val scores: List<PlacementTraceScore> = emptyList(),
) {
    fun withCapacityFilter(eliminated: List<UnschedulableReasonEntry>): PlacementTrace =
        copy(
            filters = filters + PlacementTraceFilter(name = "capacity", eliminated = eliminated),
        )

    fun withStrategy(strategy: String): PlacementTrace = copy(strategy = strategy)
}
