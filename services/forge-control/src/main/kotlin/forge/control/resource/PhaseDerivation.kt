package forge.control.resource

import java.time.Instant

/**
 * Optional pure helper controllers may call to set `status.phase`.
 *
 * Controllers may also set `status.phase` directly; this is a convenience, not a
 * required write path.
 *
 * Priority (first match wins):
 * 1. [deletionTimestamp] set → `Terminating`
 * 2. condition type `Failed` with status `True` → `Failed`
 * 3. condition type `Degraded` with status `True` → `Degraded`
 * 4. [observedGeneration] == 0 → `Pending` (never observed)
 * 5. [observedGeneration] < [generation] → `Progressing` (controller catching up)
 * 6. condition type `Ready` or `Available` with status `True` → `Ready`
 * 7. condition type `Progressing` with status `True` → `Progressing`
 * 8. else → `Pending`
 */
object PhaseDerivation {
    enum class Phase {
        Pending,
        Progressing,
        Ready,
        Degraded,
        Failed,
        Terminating,
    }

    fun derivePhase(
        conditions: List<Condition>,
        deletionTimestamp: Instant?,
        generation: Long,
        observedGeneration: Long,
    ): Phase {
        if (deletionTimestamp != null) return Phase.Terminating
        if (conditionTrue(conditions, "Failed")) return Phase.Failed
        if (conditionTrue(conditions, "Degraded")) return Phase.Degraded
        if (observedGeneration == 0L) return Phase.Pending
        if (observedGeneration < generation) return Phase.Progressing
        if (conditionTrue(conditions, "Ready") || conditionTrue(conditions, "Available")) {
            return Phase.Ready
        }
        if (conditionTrue(conditions, "Progressing")) return Phase.Progressing
        return Phase.Pending
    }

    private fun conditionTrue(conditions: List<Condition>, type: String): Boolean =
        conditions.any { it.type == type && it.status == "True" }
}
