package forge.control.scheduler

import forge.control.scheduler.model.AffinitySelector
import forge.control.scheduler.model.AffinityTerm
import forge.control.scheduler.model.UnschedulableReasonCode
import forge.control.scheduler.model.UnschedulableReasonEntry

/**
 * Hard filter for [requiredDuringScheduling] workload affinity / anti-affinity.
 *
 * Required affinity with zero matching replicas eliminates all candidates
 * (nothing to co-locate with → AffinityUnsatisfiable / pending).
 */
class WorkloadAffinityFilter(
    private val nodesById: (String) -> FleetNode?,
    private val placed: () -> List<Placement>,
) {
    constructor(nodes: NodeStore, placements: PlacementStore) : this(
        nodes::find,
        { placements.listPlaced() },
    )

    data class Result(
        val candidates: List<FleetNode>,
        val eliminated: List<UnschedulableReasonEntry>,
    )

    fun filter(candidates: List<FleetNode>, required: List<AffinityTerm>): Result {
        if (required.isEmpty() || candidates.isEmpty()) {
            return Result(candidates = candidates, eliminated = emptyList())
        }
        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        val kept = mutableListOf<FleetNode>()
        for (node in candidates) {
            val failure = required.firstNotNullOfOrNull { term ->
                evaluateTerm(node, term)
            }
            if (failure == null) {
                kept += node
            } else {
                eliminated += failure
            }
        }
        return Result(candidates = kept, eliminated = eliminated)
    }

    private fun evaluateTerm(node: FleetNode, term: AffinityTerm): UnschedulableReasonEntry? {
        val topologyKey = try {
            TopologyResolver.parseKey(term.topologyKey)
        } catch (e: IllegalArgumentException) {
            return UnschedulableReasonEntry(
                nodeId = node.id,
                reason = UnschedulableReasonCode.AffinityUnsatisfiable.wire(),
                detail = e.message ?: "invalid topologyKey",
            )
        }
        val matches = matchingPlacements(term.selector)
        val matchDomains = matches.mapNotNull { placement ->
            val host = placement.nodeId?.let(nodesById) ?: return@mapNotNull null
            TopologyResolver.resolve(host, topologyKey)
        }.toSet()
        val candidateDomain = TopologyResolver.resolve(node, topologyKey)

        return if (term.anti) {
            if (matchDomains.isEmpty()) {
                null
            } else if (candidateDomain in matchDomains) {
                UnschedulableReasonEntry(
                    nodeId = node.id,
                    reason = UnschedulableReasonCode.AffinityUnsatisfiable.wire(),
                    detail = "anti-affinity topologyKey=$topologyKey domain=$candidateDomain occupied",
                )
            } else {
                null
            }
        } else {
            when {
                matchDomains.isEmpty() ->
                    UnschedulableReasonEntry(
                        nodeId = node.id,
                        reason = UnschedulableReasonCode.AffinityUnsatisfiable.wire(),
                        detail = "required affinity has zero matching replicas",
                    )
                candidateDomain !in matchDomains ->
                    UnschedulableReasonEntry(
                        nodeId = node.id,
                        reason = UnschedulableReasonCode.AffinityUnsatisfiable.wire(),
                        detail = "affinity topologyKey=$topologyKey want one of $matchDomains have $candidateDomain",
                    )
                else -> null
            }
        }
    }

    private fun matchingPlacements(selector: AffinitySelector): List<Placement> {
        if (selector.isEmpty()) return emptyList()
        return placed().filter { placement ->
            val serviceOk = selector.service.isNullOrBlank() ||
                placement.serviceId == selector.service
            val labelsOk = selector.labels.isEmpty() ||
                selector.labels.all { (k, v) -> placement.workloadLabels[k] == v }
            serviceOk && labelsOk
        }
    }

    companion object {
        const val REASON: String = "AffinityUnsatisfiable"

        fun noop(): WorkloadAffinityFilter =
            WorkloadAffinityFilter({ null }, { emptyList() })
    }
}
