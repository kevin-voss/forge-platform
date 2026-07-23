package forge.control.scheduler

import forge.control.logging.JsonLog
import forge.control.telemetry.Telemetry
import java.time.Instant
import kotlin.math.min

/**
 * Anti-starvation: boost effective priority of long-stuck pending items so a
 * constant stream of higher-priority arrivals cannot starve them forever.
 *
 * Boost is +1 tier per [starvationSeconds] of age, bounded by [maxBoost].
 */
class PendingAgingPolicy(
    private val priorityClasses: PriorityClassStore,
    private val starvationSeconds: Long = DEFAULT_STARVATION_S,
    private val maxBoost: Int = DEFAULT_MAX_BOOST,
    private val log: JsonLog? = null,
    private val telemetry: Telemetry = Telemetry.current(),
    private val clock: () -> Instant = { Instant.now() },
) {
    data class AgedPending(
        val placement: Placement,
        val basePriority: Int,
        val ageBoost: Int,
        val effectivePriority: Int,
    )

    fun effectivePriority(placement: Placement, now: Instant = clock()): AgedPending {
        val base = priorityClasses.resolve(placement.priorityClass).value
        val ageSeconds = (now.epochSecond - placement.createdAt.epochSecond).coerceAtLeast(0)
        val boost = ageBoost(ageSeconds)
        return AgedPending(
            placement = placement,
            basePriority = base,
            ageBoost = boost,
            effectivePriority = base + boost,
        )
    }

    /**
     * Order pending items for drain: highest effective priority first, then FIFO.
     * Logs and metrics once per threshold crossing (boost > 0).
     */
    fun orderForDrain(pending: List<Placement>, now: Instant = clock()): List<Placement> {
        val aged = pending.map { effectivePriority(it, now) }
        for (item in aged) {
            if (item.ageBoost > 0) {
                log?.info(
                    "pending aged",
                    "event" to "pending_aged",
                    "placement_id" to item.placement.id,
                    "base_priority" to item.basePriority,
                    "age_boost" to item.ageBoost,
                    "effective_priority" to item.effectivePriority,
                )
                telemetry.recordPendingAged()
            }
        }
        return aged
            .sortedWith(
                compareByDescending<AgedPending> { it.effectivePriority }
                    .thenBy { it.placement.createdAt }
                    .thenBy { it.placement.replicaIndex },
            )
            .map { it.placement }
    }

    fun ageBoost(ageSeconds: Long): Int {
        if (starvationSeconds <= 0L || ageSeconds < starvationSeconds) return 0
        val tiers = (ageSeconds / starvationSeconds).toInt()
        return min(tiers, maxBoost)
    }

    companion object {
        const val DEFAULT_STARVATION_S: Long = 300
        const val DEFAULT_MAX_BOOST: Int = 10
    }
}
