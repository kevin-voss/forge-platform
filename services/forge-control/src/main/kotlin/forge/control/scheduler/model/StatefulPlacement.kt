package forge.control.scheduler.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

enum class StatefulRole {
    Primary,
    Replica,
    ;

    fun wire(): String = name.lowercase()

    companion object {
        fun parse(raw: String?): StatefulRole? {
            if (raw.isNullOrBlank()) return null
            return when (raw.trim().lowercase()) {
                "primary" -> Primary
                "replica" -> Replica
                else -> throw IllegalArgumentException(
                    "stateful.role must be primary|replica, got '$raw'",
                )
            }
        }
    }
}

enum class MigrationPolicy {
    /** Drain/preemption blocked unless an explicit migration approval exists. */
    ManualApproval,
    /** Platform may move the replica when capacity/drain requires it. */
    Auto,
    ;

    fun wire(): String = when (this) {
        ManualApproval -> "manual-approval"
        Auto -> "auto"
    }

    companion object {
        fun parse(raw: String?): MigrationPolicy {
            return when (raw?.trim()?.lowercase()) {
                null, "", "manual-approval", "manual_approval", "manual" -> ManualApproval
                "auto" -> Auto
                else -> throw IllegalArgumentException(
                    "stateful.migrationPolicy must be manual-approval|auto, got '$raw'",
                )
            }
        }
    }
}

/**
 * Stateful placement constraints (internal / platform controllers; epic 29/30 own volumes).
 */
@Serializable
data class StatefulSpec(
    @SerialName("volumeRef") val volumeRef: String? = null,
    @SerialName("volume_ref") val volumeRefSnake: String? = null,
    val role: String? = null,
    @SerialName("migrationPolicy") val migrationPolicy: String? = null,
    @SerialName("migration_policy") val migrationPolicySnake: String? = null,
    /** Stable node affinity once pinned (internal). */
    @SerialName("pinnedNodeId") val pinnedNodeId: String? = null,
    @SerialName("pinned_node_id") val pinnedNodeIdSnake: String? = null,
) {
    fun resolvedVolumeRef(): String? =
        volumeRef?.takeIf { it.isNotBlank() } ?: volumeRefSnake?.takeIf { it.isNotBlank() }

    fun resolvedRole(): StatefulRole? = StatefulRole.parse(role)

    fun resolvedMigrationPolicy(): MigrationPolicy =
        MigrationPolicy.parse(migrationPolicy ?: migrationPolicySnake)

    fun resolvedPinnedNodeId(): String? =
        pinnedNodeId?.takeIf { it.isNotBlank() }
            ?: pinnedNodeIdSnake?.takeIf { it.isNotBlank() }

    fun isEmpty(): Boolean =
        resolvedVolumeRef() == null &&
            role.isNullOrBlank() &&
            migrationPolicy.isNullOrBlank() &&
            migrationPolicySnake.isNullOrBlank() &&
            resolvedPinnedNodeId() == null

    fun isProtectedPrimary(): Boolean {
        val r = resolvedRole() ?: return false
        return r == StatefulRole.Primary &&
            resolvedMigrationPolicy() == MigrationPolicy.ManualApproval
    }
}
