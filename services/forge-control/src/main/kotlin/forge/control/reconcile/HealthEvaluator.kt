package forge.control.reconcile

enum class RolloutHealth {
    /** All desired replicas run the target image and are ready. */
    Success,
    /** Rollout still in progress within the timeout budget. */
    InProgress,
    /** Timeout elapsed with incomplete readiness, or a target replica failed. */
    Failed,
}

/**
 * Decides rollout success/failure from readiness + failures.
 * Prefer success when readiness is satisfied in the same tick as a timeout.
 */
class HealthEvaluator {
    fun evaluate(
        desired: DesiredState,
        actual: ActualState,
        timedOut: Boolean,
    ): RolloutHealth {
        val live = actual.replicas.filter { it.statusEnum() in LIVE }
        val target = live.filter { it.image == desired.image }
        val targetFailed = target.any { it.statusEnum() == ReplicaStatus.Failed }
        if (targetFailed) return RolloutHealth.Failed

        val targetReady = target.count { it.statusEnum() == ReplicaStatus.Ready }
        val allTargetReady =
            targetReady >= desired.replicas &&
                live.none { it.image != null && it.image != desired.image && it.statusEnum() in LIVE }

        if (allTargetReady) return RolloutHealth.Success
        if (timedOut) return RolloutHealth.Failed
        return RolloutHealth.InProgress
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
