package forge.control.scheduler

import forge.control.scheduler.model.TopologySpreadConstraint
import forge.control.scheduler.model.UnschedulableReasonCode
import forge.control.scheduler.model.UnschedulableReasonEntry
import forge.control.scheduler.model.WhenUnsatisfiable

/**
 * Hard filter for [TopologySpreadConstraint] with `whenUnsatisfiable=DoNotSchedule`.
 *
 * While the service has fewer than [TopologySpreadConstraint.minimumDistinctNodes]
 * distinct topology domains, candidates that reuse an existing domain are eliminated.
 */
class TopologySpreadFilter(
    private val nodesById: (String) -> FleetNode?,
    private val placedForService: (String) -> List<Placement>,
    private val defaultWhenUnsatisfiable: WhenUnsatisfiable = WhenUnsatisfiable.DoNotSchedule,
) {
    constructor(
        nodes: NodeStore,
        placements: PlacementStore,
        defaultWhenUnsatisfiable: WhenUnsatisfiable = WhenUnsatisfiable.DoNotSchedule,
    ) : this(
        nodes::find,
        { serviceId -> placements.listPlacedByService(serviceId) },
        defaultWhenUnsatisfiable,
    )

    data class Result(
        val candidates: List<FleetNode>,
        val eliminated: List<UnschedulableReasonEntry>,
        val spreadRelaxed: Boolean = false,
    )

    fun filter(
        candidates: List<FleetNode>,
        serviceId: String?,
        constraints: List<TopologySpreadConstraint>,
        hardOnly: Boolean = true,
    ): Result {
        if (constraints.isEmpty() || candidates.isEmpty() || serviceId.isNullOrBlank()) {
            return Result(candidates = candidates, eliminated = emptyList())
        }

        var current = candidates
        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        var relaxed = false

        for (constraint in constraints) {
            val whenUnsat = constraint.whenUnsatisfiableEnum(defaultWhenUnsatisfiable)
            val topologyKey = try {
                TopologyResolver.parseKey(constraint.topologyKey)
            } catch (_: IllegalArgumentException) {
                continue
            }
            val minDistinct = constraint.minimumDistinctNodes?.takeIf { it > 0 }
            val maxSkew = constraint.maxSkew?.takeIf { it >= 0 }
            if (minDistinct == null && maxSkew == null) continue

            val existing = placedForService(serviceId)
            val domainCounts = domainCounts(existing, topologyKey)
            val existingDomains = domainCounts.keys
            val fleetDomains = (existingDomains + current.map {
                TopologyResolver.resolve(it, topologyKey)
            }).toSet()

            // ScheduleAnyway never hard-filters; record relaxation when the fleet cannot
            // satisfy the constraint (or every candidate would violate it).
            if (whenUnsat == WhenUnsatisfiable.ScheduleAnyway) {
                val unsatisfiable =
                    (minDistinct != null && fleetDomains.size < minDistinct) ||
                        current.all { node ->
                            val domain = TopologyResolver.resolve(node, topologyKey)
                            val reuse = domain in existingDomains
                            val countsAfter = domainCounts.toMutableMap()
                            countsAfter[domain] = (countsAfter[domain] ?: 0) + 1
                            val violatesMin = minDistinct != null &&
                                existingDomains.size < minDistinct &&
                                reuse
                            val violatesSkew = maxSkew != null && skewOf(countsAfter) > maxSkew
                            violatesMin || violatesSkew
                        }
                if (unsatisfiable) relaxed = true
                if (hardOnly) continue
            }

            if (hardOnly && whenUnsat != WhenUnsatisfiable.DoNotSchedule) {
                continue
            }

            val stepEliminated = mutableListOf<UnschedulableReasonEntry>()
            val kept = mutableListOf<FleetNode>()
            for (node in current) {
                val domain = TopologyResolver.resolve(node, topologyKey)
                val reuse = domain in existingDomains
                val countsAfter = domainCounts.toMutableMap()
                countsAfter[domain] = (countsAfter[domain] ?: 0) + 1
                val skew = skewOf(countsAfter)

                val violatesMin = minDistinct != null &&
                    existingDomains.size < minDistinct &&
                    reuse
                val violatesSkew = maxSkew != null && skew > maxSkew

                if (violatesMin || violatesSkew) {
                    stepEliminated += UnschedulableReasonEntry(
                        nodeId = node.id,
                        reason = UnschedulableReasonCode.TopologySpreadUnsatisfiable.wire(),
                        detail = buildString {
                            append("topologyKey=$topologyKey")
                            if (violatesMin) {
                                append(" minimumDistinctNodes=$minDistinct")
                                append(" existing=${existingDomains.size}")
                            }
                            if (violatesSkew) {
                                append(" maxSkew=$maxSkew skew=$skew")
                            }
                        },
                    )
                } else {
                    kept += node
                }
            }

            if (kept.isEmpty() && whenUnsat == WhenUnsatisfiable.ScheduleAnyway) {
                relaxed = true
                // Ignore this constraint for filtering; keep prior candidates.
                continue
            }
            eliminated += stepEliminated
            current = kept
            if (current.isEmpty()) break
        }

        return Result(
            candidates = current,
            eliminated = eliminated,
            spreadRelaxed = relaxed,
        )
    }

    private fun domainCounts(placements: List<Placement>, topologyKey: String): Map<String, Int> {
        val counts = linkedMapOf<String, Int>()
        for (placement in placements) {
            val host = placement.nodeId?.let(nodesById) ?: continue
            val domain = TopologyResolver.resolve(host, topologyKey)
            counts[domain] = (counts[domain] ?: 0) + 1
        }
        return counts
    }

    private fun skewOf(counts: Map<String, Int>): Int {
        if (counts.isEmpty()) return 0
        val values = counts.values
        return (values.max() - values.min()).coerceAtLeast(0)
    }

    companion object {
        const val REASON: String = "TopologySpreadUnsatisfiable"

        fun noop(): TopologySpreadFilter =
            TopologySpreadFilter({ null }, { emptyList() })
    }
}
