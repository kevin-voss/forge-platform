package forge.control.reconcile

/**
 * Version-aware rolling update planner (07.03).
 *
 * When desired image differs from actual replica images, yields an ordered
 * start → waitReady → shift → drain → stop sequence honoring
 * `rollout.batchSize` and the minimum-available invariant:
 * `readyReplicas >= desired_replicas - batch_size`.
 */
fun needsRollingUpdate(desired: DesiredState, actual: ActualState): Boolean {
    val live = actual.replicas.filter { it.statusEnum() in SATISFYING }
    if (live.isEmpty()) return false
    return live.any { !it.image.isNullOrBlank() && it.image != desired.image }
}

fun computeRollingPlan(desired: DesiredState, actual: ActualState): ReconcilePlan {
    val target = desired.image
    val batchSize = desired.rollout.batchSize.coerceAtLeast(1)
    val minAvailable = (desired.replicas - batchSize).coerceAtLeast(0)
    val live = actual.replicas.filter { it.statusEnum() in SATISFYING }
    val updated = live.filter { it.image == target }
    val old = live.filter { it.image != target }
    val updatedReady = updated.filter { it.statusEnum() == ReplicaStatus.Ready }
    val pendingNew = updated.filter { it.statusEnum() != ReplicaStatus.Ready }
    val readyCount = live.count { it.statusEnum() == ReplicaStatus.Ready }
    val currentImage = majorityImage(live) ?: old.firstOrNull()?.image
    val rolloutMeta = rolloutMeta(
        phase = RolloutPhase.Rolling,
        updated = updated.size,
        total = desired.replicas,
        currentImage = currentImage,
        targetImage = target,
    )

    if (!needsRollingUpdate(desired, actual)) {
        val base = computePlan(desired, actual)
        val phase = when {
            base.actions.isNotEmpty() -> RolloutPhase.Converging
            else -> RolloutPhase.Idle
        }
        return base.withRollout(
            phase = phase,
            updatedReplicas = live.count { it.image == null || it.image == target },
            totalReplicas = desired.replicas,
            currentImage = majorityImage(live) ?: desired.image,
            targetImage = desired.image,
        )
    }

    // Hold: new replicas started but not yet ready — wait only (no stop).
    if (pendingNew.isNotEmpty()) {
        val actions = pendingNew
            .sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }
            .take(batchSize)
            .map { replica ->
                ReconcileActionItem(
                    action = ReconcileAction.WaitReady.name,
                    reason = "rolling wait ready image=$target",
                    replicaId = (replica.resolvedIndex() ?: replica.replicaId).toString(),
                )
            }
        return ReconcilePlan(actions = actions).withRollout(rolloutMeta)
    }

    // Continue wave: updated replicas are ready, old still present → shift/drain/stop.
    if (updatedReady.isNotEmpty() && old.isNotEmpty()) {
        val actions = mutableListOf<ReconcileActionItem>()
        val readyNew = updatedReady
            .sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }
            .take(batchSize)
        for (replica in readyNew) {
            actions += ReconcileActionItem(
                action = ReconcileAction.ShiftTraffic.name,
                reason = "rolling shift to image=$target",
                replicaId = (replica.resolvedIndex() ?: replica.replicaId).toString(),
            )
        }
        val stopBudget = stopBudget(
            readyCount = readyCount,
            minAvailable = minAvailable,
            batchSize = batchSize,
            oldCount = old.size,
        )
        val stopTargets = old
            .sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }
            .take(stopBudget)
        for (replica in stopTargets) {
            val id = (replica.resolvedIndex() ?: replica.replicaId).toString()
            actions += ReconcileActionItem(
                action = ReconcileAction.DrainReplica.name,
                reason = "rolling drain old image=${replica.image}",
                replicaId = id,
            )
            actions += ReconcileActionItem(
                action = ReconcileAction.StopReplica.name,
                reason = "rolling stop old image=${replica.image}",
                replicaId = id,
            )
        }
        return ReconcilePlan(actions = actions).withRollout(rolloutMeta).also {
            assertStopInvariant(it, readyCount, minAvailable)
        }
    }

    // Start surge replicas for the next wave (starts before any stops).
    val need = (desired.replicas - updated.size).coerceAtLeast(0)
    val maxTotal = desired.replicas + batchSize
    val canStart = minOf(need, batchSize, (maxTotal - live.size).coerceAtLeast(0))
    if (canStart <= 0) {
        // Nothing to start and nothing ready to shift — hold (e.g. waiting externally).
        return ReconcilePlan.EMPTY.withRollout(rolloutMeta)
    }

    val used = live.mapNotNull { it.resolvedIndex() }.toSet()
    var nextIndex = 0
    val startIndices = mutableListOf<Int>()
    repeat(canStart) {
        while (nextIndex in used || nextIndex in startIndices) nextIndex++
        startIndices += nextIndex
        nextIndex++
    }

    val actions = mutableListOf<ReconcileActionItem>()
    for (index in startIndices) {
        actions += ReconcileActionItem(
            action = ReconcileAction.StartReplica.name,
            reason = "rolling start image=$target",
            replicaId = index.toString(),
        )
    }
    for (index in startIndices) {
        actions += ReconcileActionItem(
            action = ReconcileAction.WaitReady.name,
            reason = "rolling wait ready image=$target",
            replicaId = index.toString(),
        )
    }
    for (index in startIndices) {
        actions += ReconcileActionItem(
            action = ReconcileAction.ShiftTraffic.name,
            reason = "rolling shift to image=$target",
            replicaId = index.toString(),
        )
    }

    // Projected ready count after new replicas become ready.
    val projectedReady = readyCount + startIndices.size
    val stopBudget = stopBudget(
        readyCount = projectedReady,
        minAvailable = minAvailable,
        batchSize = batchSize,
        oldCount = old.size,
    )
    val stopTargets = old
        .sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }
        .take(stopBudget)
    for (replica in stopTargets) {
        val id = (replica.resolvedIndex() ?: replica.replicaId).toString()
        actions += ReconcileActionItem(
            action = ReconcileAction.DrainReplica.name,
            reason = "rolling drain old image=${replica.image}",
            replicaId = id,
        )
        actions += ReconcileActionItem(
            action = ReconcileAction.StopReplica.name,
            reason = "rolling stop old image=${replica.image}",
            replicaId = id,
        )
    }

    val plan = ReconcilePlan(actions = actions).withRollout(rolloutMeta)
    assertStopInvariant(plan, projectedReady, minAvailable)
    return plan
}

/** Choose rolling or single-version plan. */
fun computeReconcilePlan(desired: DesiredState, actual: ActualState): ReconcilePlan =
    if (needsRollingUpdate(desired, actual)) {
        computeRollingPlan(desired, actual)
    } else {
        computeRollingPlan(desired, actual) // attaches idle/converging metadata via non-rolling branch
    }

private fun stopBudget(
    readyCount: Int,
    minAvailable: Int,
    batchSize: Int,
    oldCount: Int,
): Int {
    // After each stop, ready decreases by 1. Allow stops while ready - k >= minAvailable.
    val maxByInvariant = (readyCount - minAvailable).coerceAtLeast(0)
    return minOf(batchSize, oldCount, maxByInvariant)
}

private fun assertStopInvariant(plan: ReconcilePlan, readyCount: Int, minAvailable: Int) {
    var ready = readyCount
    for (action in plan.actions) {
        if (action.action == ReconcileAction.StopReplica.name) {
            check(ready - 1 >= minAvailable) {
                "rolling plan would drop ready below minAvailable=$minAvailable"
            }
            ready--
        }
    }
}

private fun majorityImage(replicas: List<ReplicaObservation>): String? {
    if (replicas.isEmpty()) return null
    return replicas.mapNotNull { it.image?.takeIf { img -> img.isNotBlank() } }
        .groupingBy { it }
        .eachCount()
        .maxByOrNull { it.value }
        ?.key
}

private fun rolloutMeta(
    phase: RolloutPhase,
    updated: Int,
    total: Int,
    currentImage: String?,
    targetImage: String?,
): RolloutMeta =
    RolloutMeta(
        phase = phase,
        updatedReplicas = updated,
        totalReplicas = total,
        currentImage = currentImage,
        targetImage = targetImage,
    )

private data class RolloutMeta(
    val phase: RolloutPhase,
    val updatedReplicas: Int,
    val totalReplicas: Int,
    val currentImage: String?,
    val targetImage: String?,
)

private fun ReconcilePlan.withRollout(meta: RolloutMeta): ReconcilePlan =
    withRollout(
        phase = meta.phase,
        updatedReplicas = meta.updatedReplicas,
        totalReplicas = meta.totalReplicas,
        currentImage = meta.currentImage,
        targetImage = meta.targetImage,
    )

private fun ReconcilePlan.withRollout(
    phase: RolloutPhase,
    updatedReplicas: Int,
    totalReplicas: Int,
    currentImage: String?,
    targetImage: String?,
): ReconcilePlan =
    copy(
        phase = phase.wire(),
        updatedReplicas = updatedReplicas,
        totalReplicas = totalReplicas,
        currentImage = currentImage,
        targetImage = targetImage,
    )

private val SATISFYING = setOf(
    ReplicaStatus.Pending,
    ReplicaStatus.Running,
    ReplicaStatus.Ready,
)
