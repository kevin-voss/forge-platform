package forge.control.scheduler

import forge.control.scheduler.model.AffinitySelector
import forge.control.scheduler.model.PreferredAffinityTerm

/**
 * Soft (preferred) affinity / anti-affinity score component.
 */
object SoftAffinityScorer {
    fun score(
        node: FleetNode,
        preferred: List<PreferredAffinityTerm>,
        nodesById: (String) -> FleetNode?,
        placed: () -> List<Placement>,
    ): Double {
        if (preferred.isEmpty()) return 0.0
        var total = 0.0
        for (term in preferred) {
            val weight = term.weight.coerceAtLeast(0).toDouble()
            if (weight == 0.0) continue
            val topologyKey = try {
                TopologyResolver.parseKey(term.topologyKey)
            } catch (_: IllegalArgumentException) {
                continue
            }
            val matches = matchingPlacements(term.selector, placed())
            if (matches.isEmpty()) continue
            val matchDomains = matches.mapNotNull { placement ->
                val host = placement.nodeId?.let(nodesById) ?: return@mapNotNull null
                TopologyResolver.resolve(host, topologyKey)
            }.toSet()
            if (matchDomains.isEmpty()) continue
            val candidateDomain = TopologyResolver.resolve(node, topologyKey)
            val matchesDomain = candidateDomain in matchDomains
            total += if (term.anti) {
                if (matchesDomain) 0.0 else weight
            } else {
                if (matchesDomain) weight else 0.0
            }
        }
        return total
    }

    private fun matchingPlacements(selector: AffinitySelector, placed: List<Placement>): List<Placement> {
        if (selector.isEmpty()) return emptyList()
        return placed.filter { placement ->
            val serviceOk = selector.service.isNullOrBlank() ||
                placement.serviceId == selector.service
            val labelsOk = selector.labels.isEmpty() ||
                selector.labels.all { (k, v) -> placement.workloadLabels[k] == v }
            serviceOk && labelsOk
        }
    }
}
