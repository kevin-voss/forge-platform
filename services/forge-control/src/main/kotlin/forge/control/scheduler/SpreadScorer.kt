package forge.control.scheduler

import forge.control.scheduler.model.TopologySpreadConstraint

/**
 * Score component rewarding candidates in under-represented topology domains.
 */
object SpreadScorer {
    /**
     * Higher is better. For each constraint, adds `(maxCount - countInDomain)` so empty
     * domains outrank domains already holding replicas.
     */
    fun score(
        node: FleetNode,
        serviceId: String?,
        constraints: List<TopologySpreadConstraint>,
        nodesById: (String) -> FleetNode?,
        placedForService: (String) -> List<Placement>,
    ): Double {
        if (serviceId.isNullOrBlank() || constraints.isEmpty()) return 0.0
        val existing = placedForService(serviceId)
        var total = 0.0
        for (constraint in constraints) {
            val topologyKey = try {
                TopologyResolver.parseKey(constraint.topologyKey)
            } catch (_: IllegalArgumentException) {
                continue
            }
            val counts = linkedMapOf<String, Int>()
            for (placement in existing) {
                val host = placement.nodeId?.let(nodesById) ?: continue
                val domain = TopologyResolver.resolve(host, topologyKey)
                counts[domain] = (counts[domain] ?: 0) + 1
            }
            val domain = TopologyResolver.resolve(node, topologyKey)
            val inDomain = counts[domain] ?: 0
            val maxCount = counts.values.maxOrNull() ?: 0
            total += (maxCount - inDomain + if (domain !in counts) 1 else 0).toDouble()
        }
        return total
    }
}
