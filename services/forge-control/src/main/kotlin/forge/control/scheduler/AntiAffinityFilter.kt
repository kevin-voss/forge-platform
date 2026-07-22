package forge.control.scheduler

/**
 * Prefer (or require) nodes that do not already run a replica of the same service.
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
    }
}
