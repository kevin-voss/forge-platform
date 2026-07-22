package forge.control.reconcile

import kotlinx.serialization.Serializable

/** Observed replica status vocabulary for reconciliation (07.01). */
enum class ReplicaStatus {
    Pending,
    Running,
    Ready,
    Failed,
    Stopped,
    ;

    fun wire(): String = name.lowercase()

    companion object {
        fun parse(raw: String): ReplicaStatus =
            when (raw.trim().lowercase()) {
                "pending", "starting" -> Pending
                "running" -> Running
                "ready" -> Ready
                "failed", "unhealthy" -> Failed
                "stopped" -> Stopped
                else -> throw IllegalArgumentException("unknown replica status: $raw")
            }
    }
}

@Serializable
data class ReplicaObservation(
    val replicaId: String,
    val status: String,
) {
    init {
        require(replicaId.isNotBlank()) { "replicaId must not be blank" }
        ReplicaStatus.parse(status)
    }

    fun statusEnum(): ReplicaStatus = ReplicaStatus.parse(status)
}

/** Observed replicas for a deployment (from Runtime). */
@Serializable
data class ActualState(
    val replicas: List<ReplicaObservation> = emptyList(),
)
