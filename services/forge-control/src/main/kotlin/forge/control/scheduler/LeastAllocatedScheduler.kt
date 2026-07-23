package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest

/**
 * Place on the online node with the most free slots (tie → lowest id),
 * preferring nodes without a same-service replica when anti-affinity applies.
 */
class LeastAllocatedScheduler(
    private val nodes: NodeStore,
    private val reservation: CapacityReservation,
    private val antiAffinity: AntiAffinityFilter = AntiAffinityFilter.noop(),
    private val onSoftFallback: (() -> Unit)? = null,
    private val strictNodeSelector: Boolean = false,
) : Scheduler {
    override fun place(request: PlacementRequest): PlacementDecision =
        CapacityAwarePlacement.place(
            nodes = nodes,
            reservation = reservation,
            request = request,
            strategy = STRATEGY,
            antiAffinity = antiAffinity,
            onSoftFallback = onSoftFallback,
            pick = { candidates ->
                candidates.minWith(
                    compareByDescending<FleetNode> { PlacementCapacity.freeSlots(it) }
                        .thenBy { it.id },
                )
            },
            reasonFor = { chosen, freeBefore ->
                "least-allocated: ${chosen.id} free=$freeBefore"
            },
            strictNodeSelector = strictNodeSelector,
        )

    companion object {
        const val STRATEGY: String = "least-allocated"
    }
}
