package forge.control.reconcile

/**
 * Plans and evaluates automatic rollback to [LastHealthyDeployment].
 *
 * Produces stop-target + restore-old actions. Idempotent when partially applied:
 * already-stopped target replicas and already-present healthy replicas are skipped.
 * Status transitions into `rolling_back` / `rolled_back` go through
 * [TransitionRecorder] so history survives controller restarts mid-rollback.
 */
class Rollbacker {
    fun planRollback(
        desired: DesiredState,
        actual: ActualState,
        lastHealthy: LastHealthyDeployment,
        failedTargetImage: String? = null,
    ): ReconcilePlan {
        val restoreImage = lastHealthy.image
        val targetImage = failedTargetImage?.takeIf { it.isNotBlank() && it != restoreImage }
            ?: desired.image.takeIf { it != restoreImage }
        val restoreReplicas = maxOf(lastHealthy.replicas, desired.replicas)
        val live = actual.replicas.filter { it.statusEnum() in LIVE }
        val targetReplicas = live.filter { replica ->
            val image = replica.image
            image != null && image != restoreImage && (targetImage == null || image == targetImage)
        }
        val healthyReplicas = live.filter { it.image == restoreImage }
        val healthyReady = healthyReplicas.filter { it.statusEnum() == ReplicaStatus.Ready }
        val actions = mutableListOf<ReconcileActionItem>()

        // Stop failed/new target-version replicas first (never stop the last ready healthy ones).
        for (replica in targetReplicas.sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }) {
            val id = (replica.resolvedIndex() ?: replica.replicaId).toString()
            val image = replica.image ?: targetImage ?: "unknown"
            actions += ReconcileActionItem(
                action = ReconcileAction.DrainReplica.name,
                reason = "rollback drain target image=$image",
                replicaId = id,
            )
            actions += ReconcileActionItem(
                action = ReconcileAction.StopReplica.name,
                reason = "rollback stop target image=$image",
                replicaId = id,
            )
        }

        val need = (restoreReplicas - healthyReplicas.size).coerceAtLeast(0)
        if (need > 0) {
            val used = live.mapNotNull { it.resolvedIndex() }.toSet()
            var next = 0
            repeat(need) {
                while (next in used) next++
                val index = next
                next++
                actions += ReconcileActionItem(
                    action = ReconcileAction.StartReplica.name,
                    reason = "rollback restore image=$restoreImage",
                    replicaId = index.toString(),
                )
                actions += ReconcileActionItem(
                    action = ReconcileAction.WaitReady.name,
                    reason = "rollback wait ready image=$restoreImage",
                    replicaId = index.toString(),
                )
            }
        }

        for (replica in healthyReady.sortedBy { it.resolvedIndex() ?: Int.MAX_VALUE }) {
            val id = (replica.resolvedIndex() ?: replica.replicaId).toString()
            actions += ReconcileActionItem(
                action = ReconcileAction.ShiftTraffic.name,
                reason = "rollback shift to image=$restoreImage",
                replicaId = id,
            )
        }

        return ReconcilePlan(
            actions = actions,
            phase = RolloutPhase.Degraded.wire(),
            updatedReplicas = healthyReady.size,
            totalReplicas = restoreReplicas,
            currentImage = targetImage ?: live.firstOrNull()?.image,
            targetImage = restoreImage,
        )
    }

    fun isRestored(
        actual: ActualState,
        lastHealthy: LastHealthyDeployment,
    ): Boolean {
        val live = actual.replicas.filter { it.statusEnum() in LIVE }
        if (live.any { it.image != null && it.image != lastHealthy.image }) return false
        val ready = live.count {
            it.image == lastHealthy.image && it.statusEnum() == ReplicaStatus.Ready
        }
        return ready >= lastHealthy.replicas
    }

    companion object {
        private val LIVE = setOf(
            ReplicaStatus.Pending,
            ReplicaStatus.Running,
            ReplicaStatus.Ready,
            ReplicaStatus.Failed,
        )
    }
}
