package forge.control.reconcile

import kotlinx.serialization.Serializable

enum class ReconcileAction {
    StartReplica,
    StopReplica,
    WaitReady,
    ShiftTraffic,
    DrainReplica,
    NoOp,
}

enum class RolloutPhase {
    Idle,
    Converging,
    Rolling,
    Degraded,
    ;

    fun wire(): String = name.lowercase()

    companion object {
        fun parse(raw: String?): RolloutPhase =
            when (raw?.trim()?.lowercase()) {
                null, "", "idle" -> Idle
                "converging" -> Converging
                "rolling" -> Rolling
                "degraded" -> Degraded
                else -> Idle
            }
    }
}

@Serializable
data class ReconcileActionItem(
    val action: String,
    val reason: String,
    val replicaId: String? = null,
) {
    init {
        require(action in ACTIONS) { "unknown reconcile action: $action" }
        require(reason.isNotBlank()) { "reason must not be blank" }
    }

    companion object {
        private val ACTIONS = setOf(
            ReconcileAction.StartReplica.name,
            ReconcileAction.StopReplica.name,
            ReconcileAction.WaitReady.name,
            ReconcileAction.ShiftTraffic.name,
            ReconcileAction.DrainReplica.name,
            ReconcileAction.NoOp.name,
        )
    }
}

/** Ordered list of intended actions plus rollout progress metadata. */
@Serializable
data class ReconcilePlan(
    val actions: List<ReconcileActionItem> = emptyList(),
    val phase: String = RolloutPhase.Idle.wire(),
    val updatedReplicas: Int = 0,
    val totalReplicas: Int = 0,
    val currentImage: String? = null,
    val targetImage: String? = null,
) {
    val size: Int get() = actions.size

    fun phaseEnum(): RolloutPhase = RolloutPhase.parse(phase)

    companion object {
        val EMPTY = ReconcilePlan()
    }
}
