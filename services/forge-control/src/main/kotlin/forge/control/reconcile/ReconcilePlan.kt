package forge.control.reconcile

import kotlinx.serialization.Serializable

enum class ReconcileAction {
    StartReplica,
    StopReplica,
    NoOp,
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
            ReconcileAction.NoOp.name,
        )
    }
}

/** Ordered list of intended actions — not executed in 07.01. */
@Serializable
data class ReconcilePlan(
    val actions: List<ReconcileActionItem> = emptyList(),
) {
    val size: Int get() = actions.size

    companion object {
        val EMPTY = ReconcilePlan()
    }
}
