package forge.control.reconcile

/**
 * Pure desired-vs-actual diff for epic 07.
 *
 * Counts only replicas that satisfy desired capacity (`pending`/`running`/`ready`).
 * `failed` and `stopped` do not satisfy desired; crashed slots are preferred
 * recreate targets (StartReplica with that index) before allocating new indices.
 * Scale-down stops the highest-index satisfying replica first.
 */
fun computePlan(desired: DesiredState, actual: ActualState): ReconcilePlan {
    val satisfying = actual.replicas.filter { it.statusEnum() in SATISFYING }
    val satisfyingCount = satisfying.size
    val delta = desired.replicas - satisfyingCount
    val actions = mutableListOf<ReconcileActionItem>()

    when {
        delta > 0 -> {
            val crashedIndices = CrashDetector.crashedReplicas(actual)
                .mapNotNull { it.resolvedIndex() }
                .filter { it in 0 until desired.replicas }
                .distinct()
                .sorted()
                .toMutableList()
            val usedSatisfying = satisfying.mapNotNull { it.resolvedIndex() }.toSet()
            var nextNew = 0
            repeat(delta) {
                val index = if (crashedIndices.isNotEmpty()) {
                    crashedIndices.removeAt(0)
                } else {
                    while (nextNew in usedSatisfying || nextNew in crashedIndices) {
                        nextNew++
                    }
                    val chosen = nextNew
                    nextNew++
                    chosen
                }
                actions += ReconcileActionItem(
                    action = ReconcileAction.StartReplica.name,
                    reason = "desired=${desired.replicas} actual=$satisfyingCount",
                    replicaId = index.toString(),
                )
            }
        }
        delta < 0 -> {
            val stopCount = -delta
            val ordered = satisfying.sortedByDescending {
                it.resolvedIndex() ?: Int.MIN_VALUE
            }
            for (target in ordered.take(stopCount)) {
                val index = target.resolvedIndex()
                actions += ReconcileActionItem(
                    action = ReconcileAction.StopReplica.name,
                    reason = "desired=${desired.replicas} actual=$satisfyingCount",
                    replicaId = index?.toString() ?: target.replicaId,
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
