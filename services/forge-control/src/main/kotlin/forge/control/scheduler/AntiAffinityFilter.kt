package forge.control.scheduler

import forge.control.scheduler.model.AffinitySelector
import forge.control.scheduler.model.AffinityTerm
import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PreferredAffinityTerm

/**
 * Legacy same-service / node-level anti-affinity from epic 08.04.
 *
 * Reimplemented as sugar for `topologyKey=node` anti-affinity so callers that only
 * set `anti_affinity: soft|hard` keep identical behavior.
 */
class AntiAffinityFilter(
    private val occupiedNodes: (serviceId: String) -> Set<String>,
) {
    constructor(store: PlacementStore) : this(store::nodeIdsWithPlacedService)

    /**
     * Among [candidates], return those without an existing placed replica of [serviceId].
     * When [serviceId] is blank, all candidates are preferred (no filter).
     */
    fun filterPreferred(serviceId: String?, candidates: List<FleetNode>): List<FleetNode> {
        if (serviceId.isNullOrBlank() || candidates.isEmpty()) return candidates
        val occupied = occupiedNodes(serviceId)
        if (occupied.isEmpty()) return candidates
        return candidates.filter { it.id !in occupied }
    }

    companion object {
        fun noop(): AntiAffinityFilter = AntiAffinityFilter({ emptySet() })

        /** Hard sugar term for legacy `anti_affinity: hard`. */
        fun hardTerm(serviceId: String?): AffinityTerm? {
            if (serviceId.isNullOrBlank()) return null
            return AffinityTerm(
                selector = AffinitySelector(service = serviceId),
                topologyKey = TopologyResolver.KEY_NODE,
                anti = true,
            )
        }

        /** Soft sugar term for legacy `anti_affinity: soft`. */
        fun softTerm(serviceId: String?): PreferredAffinityTerm? {
            if (serviceId.isNullOrBlank()) return null
            return PreferredAffinityTerm(
                weight = 100,
                selector = AffinitySelector(service = serviceId),
                topologyKey = TopologyResolver.KEY_NODE,
                anti = true,
            )
        }

        fun expandRequired(
            antiAffinity: AntiAffinity,
            serviceId: String?,
            explicit: List<AffinityTerm>,
        ): List<AffinityTerm> {
            val legacy = if (antiAffinity == AntiAffinity.Hard) {
                listOfNotNull(hardTerm(serviceId))
            } else {
                emptyList()
            }
            return explicit + legacy
        }

        fun expandPreferred(
            antiAffinity: AntiAffinity,
            serviceId: String?,
            explicit: List<PreferredAffinityTerm>,
        ): List<PreferredAffinityTerm> {
            val legacy = if (antiAffinity == AntiAffinity.Soft) {
                listOfNotNull(softTerm(serviceId))
            } else {
                emptyList()
            }
            return explicit + legacy
        }
    }
}
