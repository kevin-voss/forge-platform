package forge.control.resource

import java.time.Instant

/**
 * Merge helpers for [Condition] arrays inside `status.conditions`.
 *
 * Replaces the same-[Condition.type] entry. [Condition.lastTransitionTime] updates
 * only when [Condition.status] actually changes value; otherwise it is preserved and
 * [Condition.reason]/[Condition.message] update in place.
 */
object ConditionMerge {
    fun mergeCondition(
        existing: Condition?,
        next: Condition,
        now: Instant = Instant.now(),
    ): Condition {
        if (existing == null) {
            return next.copy(lastTransitionTime = next.lastTransitionTime ?: now.toString())
        }
        val statusChanged = existing.status != next.status
        return next.copy(
            lastTransitionTime = if (statusChanged) {
                now.toString()
            } else {
                existing.lastTransitionTime ?: next.lastTransitionTime ?: now.toString()
            },
        )
    }

    /**
     * Applies each [incoming] condition onto [existing] by type (order of [incoming]
     * preserved for new types; untouched existing types are kept).
     */
    fun mergeConditions(
        existing: List<Condition>,
        incoming: List<Condition>,
        now: Instant = Instant.now(),
    ): List<Condition> {
        if (incoming.isEmpty()) return existing
        val byType = existing.associateBy { it.type }.toMutableMap()
        val order = existing.map { it.type }.toMutableList()
        for (next in incoming) {
            val merged = mergeCondition(byType[next.type], next, now)
            if (next.type !in byType) {
                order.add(next.type)
            }
            byType[next.type] = merged
        }
        return order.mapNotNull { byType[it] }
    }
}
