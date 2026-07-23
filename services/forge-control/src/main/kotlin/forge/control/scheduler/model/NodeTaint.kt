package forge.control.scheduler.model

import kotlinx.serialization.Serializable

enum class TaintEffect {
    NoSchedule,
    NoExecute,
    ;

    fun wire(): String = name

    companion object {
        fun parse(raw: String?): TaintEffect? {
            if (raw.isNullOrBlank()) return null
            return when (raw.trim()) {
                "NoSchedule" -> NoSchedule
                "NoExecute" -> NoExecute
                else -> throw IllegalArgumentException(
                    "taint effect must be NoSchedule|NoExecute, got '$raw'",
                )
            }
        }
    }
}

enum class TolerationOperator {
    Equal,
    Exists,
    ;

    fun wire(): String = name

    companion object {
        fun parse(raw: String?): TolerationOperator =
            when (raw?.trim()?.lowercase()) {
                null, "", "equal" -> Equal
                "exists" -> Exists
                else -> throw IllegalArgumentException(
                    "toleration operator must be Equal|Exists, got '$raw'",
                )
            }
    }
}

@Serializable
data class NodeTaint(
    val key: String,
    val value: String? = null,
    val effect: String,
) {
    fun effectEnum(): TaintEffect =
        TaintEffect.parse(effect) ?: error("taint effect required")

    fun sameAs(other: NodeTaint): Boolean =
        key == other.key && value == other.value && effect == other.effect
}

@Serializable
data class Toleration(
    val key: String,
    val operator: String = "Equal",
    val value: String? = null,
    val effect: String? = null,
) {
    fun operatorEnum(): TolerationOperator = TolerationOperator.parse(operator)

    fun effectEnum(): TaintEffect? = TaintEffect.parse(effect)

    fun matches(taint: NodeTaint): Boolean {
        if (key != taint.key) return false
        val effectWanted = effectEnum()
        if (effectWanted != null && effectWanted.wire() != taint.effect) return false
        return when (operatorEnum()) {
            TolerationOperator.Exists -> true
            TolerationOperator.Equal -> (value ?: "") == (taint.value ?: "")
        }
    }
}

@Serializable
data class PlatformSpec(
    val architecture: String? = null,
    val os: String? = null,
) {
    fun isEmpty(): Boolean = architecture.isNullOrBlank() && os.isNullOrBlank()
}

@Serializable
data class PlacementSpec(
    val nodeSelector: Map<String, String> = emptyMap(),
    val tolerations: List<Toleration> = emptyList(),
    val affinity: PlacementAffinity? = null,
    val topologySpreadConstraints: List<TopologySpreadConstraint> = emptyList(),
)
