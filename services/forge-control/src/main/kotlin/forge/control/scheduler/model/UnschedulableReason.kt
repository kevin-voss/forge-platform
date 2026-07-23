package forge.control.scheduler.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

enum class UnschedulableReasonCode {
    InsufficientCPU,
    InsufficientMemory,
    InsufficientDisk,
    InsufficientSlots,
    ;

    fun wire(): String = name
}

@Serializable
data class UnschedulableReasonEntry(
    @SerialName("node_id") val nodeId: String,
    val reason: String,
    val detail: String,
)

object UnschedulableReasons {
    fun entry(
        nodeId: String,
        code: UnschedulableReasonCode,
        requested: Int,
        free: Int,
        unit: String = "",
    ): UnschedulableReasonEntry {
        val unitSuffix = if (unit.isBlank()) "" else " $unit"
        return UnschedulableReasonEntry(
            nodeId = nodeId,
            reason = code.wire(),
            detail = "requested $requested$unitSuffix, free $free$unitSuffix".trim(),
        )
    }

    fun summarize(entries: List<UnschedulableReasonEntry>): String {
        if (entries.isEmpty()) return "no node available"
        val best = entries
            .groupBy { it.reason }
            .maxByOrNull { it.value.size }
            ?.value
            ?.first()
            ?: entries.first()
        return "${best.reason}: ${best.detail}"
    }
}
