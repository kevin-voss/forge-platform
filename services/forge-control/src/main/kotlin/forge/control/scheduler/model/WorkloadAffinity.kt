package forge.control.scheduler.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

enum class WhenUnsatisfiable {
    DoNotSchedule,
    ScheduleAnyway,
    ;

    fun wire(): String = name

    companion object {
        fun parse(raw: String?, default: WhenUnsatisfiable = DoNotSchedule): WhenUnsatisfiable {
            if (raw.isNullOrBlank()) return default
            return when (raw.trim()) {
                "DoNotSchedule" -> DoNotSchedule
                "ScheduleAnyway" -> ScheduleAnyway
                else -> throw IllegalArgumentException(
                    "whenUnsatisfiable must be DoNotSchedule|ScheduleAnyway, got '$raw'",
                )
            }
        }
    }
}

@Serializable
data class AffinitySelector(
    val service: String? = null,
    val labels: Map<String, String> = emptyMap(),
) {
    fun isEmpty(): Boolean = service.isNullOrBlank() && labels.isEmpty()
}

@Serializable
data class AffinityTerm(
    val selector: AffinitySelector = AffinitySelector(),
    val topologyKey: String = "node",
    /** When true, this term is anti-affinity (avoid co-location). */
    val anti: Boolean = false,
)

@Serializable
data class PreferredAffinityTerm(
    val weight: Int = 1,
    val selector: AffinitySelector = AffinitySelector(),
    val topologyKey: String = "node",
    val anti: Boolean = false,
)

@Serializable
data class AffinityRules(
    val requiredDuringScheduling: List<AffinityTerm> = emptyList(),
    val preferredDuringScheduling: List<PreferredAffinityTerm> = emptyList(),
) {
    fun isEmpty(): Boolean =
        requiredDuringScheduling.isEmpty() && preferredDuringScheduling.isEmpty()
}

@Serializable
data class PlacementAffinity(
    val workload: AffinityRules? = null,
    /** Anti-affinity rules (arbitrary selectors / topology keys). */
    @SerialName("workloadAnti")
    val workloadAnti: AffinityRules? = null,
) {
    fun isEmpty(): Boolean =
        (workload == null || workload.isEmpty()) &&
            (workloadAnti == null || workloadAnti.isEmpty())

    fun requiredTerms(): List<AffinityTerm> =
        (workload?.requiredDuringScheduling.orEmpty()) +
            workloadAnti?.requiredDuringScheduling.orEmpty().map { it.copy(anti = true) }

    fun preferredTerms(): List<PreferredAffinityTerm> =
        (workload?.preferredDuringScheduling.orEmpty()) +
            workloadAnti?.preferredDuringScheduling.orEmpty().map { it.copy(anti = true) }
}

@Serializable
data class TopologySpreadConstraint(
    val topologyKey: String = "node",
    val minimumDistinctNodes: Int? = null,
    val maxSkew: Int? = null,
    val whenUnsatisfiable: String = "DoNotSchedule",
) {
    fun whenUnsatisfiableEnum(default: WhenUnsatisfiable = WhenUnsatisfiable.DoNotSchedule): WhenUnsatisfiable =
        WhenUnsatisfiable.parse(whenUnsatisfiable, default)
}

@Serializable
data class NodeTopology(
    val node: String,
    val zone: String = "default",
    val region: String = "default",
    val provider: String = "docker",
)
