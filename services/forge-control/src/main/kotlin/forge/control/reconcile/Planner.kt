package forge.control.reconcile

/**
 * Pure desired-vs-actual diff for epic 07.01.
 *
 * Counts only replicas that satisfy desired capacity (`pending`/`running`/`ready`).
 * `failed` and `stopped` do not satisfy desired and are preferred stop targets when
 * scaling down. Rolling-update logic arrives in later steps.
 */
fun computePlan(desired: DesiredState, actual: ActualState): ReconcilePlan {
    val satisfying = actual.replicas.filter { it.statusEnum() in SATISFYING }
    val satisfyingCount = satisfying.size
    val delta = desired.replicas - satisfyingCount
    val actions = mutableListOf<ReconcileActionItem>()

    when {
        delta > 0 -> {
            repeat(delta) {
                actions += ReconcileActionItem(
                    action = ReconcileAction.StartReplica.name,
                    reason = "desired=${desired.replicas} actual=$satisfyingCount",
                )
            }
        }
        delta < 0 -> {
            val stopCount = -delta
            // Scale-down removes excess satisfying replicas (newest first).
            for (target in satisfying.asReversed().take(stopCount)) {
                actions += ReconcileActionItem(
                    action = ReconcileAction.StopReplica.name,
                    reason = "desired=${desired.replicas} actual=$satisfyingCount",
                    replicaId = target.replicaId,
                )
            }
        }
    }

    return ReconcilePlan(actions)
}

private val SATISFYING = setOf(
    ReplicaStatus.Pending,
    ReplicaStatus.Running,
    ReplicaStatus.Ready,
)
