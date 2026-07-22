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
    val replicaIndex: Int? = null,
    val restartCount: Int = 0,
    val workloadName: String? = null,
    val image: String? = null,
) {
    init {
        require(replicaId.isNotBlank()) { "replicaId must not be blank" }
        ReplicaStatus.parse(status)
        require(restartCount >= 0) { "restartCount must be >= 0" }
        if (replicaIndex != null) {
            require(replicaIndex >= 0) { "replicaIndex must be >= 0" }
        }
    }

    fun statusEnum(): ReplicaStatus = ReplicaStatus.parse(status)

    fun resolvedIndex(): Int? =
        replicaIndex ?: WorkloadNamer.parseReplicaIndex(replicaId)
}

/** Observed replicas for a deployment (from Runtime). */
@Serializable
data class ActualState(
    val replicas: List<ReplicaObservation> = emptyList(),
)
