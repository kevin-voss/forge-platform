package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest

/**
 * Place on the online node with the most free slots (tie → lowest id).
 */
class LeastAllocatedScheduler(
    private val nodes: NodeStore,
    private val reservation: CapacityReservation,
) : Scheduler {
    override fun place(request: PlacementRequest): PlacementDecision {
        val excluded = linkedSetOf<String>()
        while (true) {
            val candidates = PlacementCapacity.candidates(nodes, request.requirements, excluded)
            if (candidates.isEmpty()) {
                return PlacementDecision.NoNodeAvailable(
                    reason = if (excluded.isEmpty()) {
                        "no node available"
                    } else {
                        "no node available after reservation retries"
                    },
                )
            }
            val chosen = candidates.maxWithOrNull(
                compareBy<FleetNode> { PlacementCapacity.freeSlots(it) }
                    .thenByDescending { it.id },
            )!!.let { best ->
                // maxBy free.slots, then lowest id on ties → invert id for maxWith
                candidates
                    .filter { PlacementCapacity.freeSlots(it) == PlacementCapacity.freeSlots(best) }
                    .minBy { it.id }
            }
            val freeBefore = PlacementCapacity.freeSlots(chosen)
            if (!reservation.tryReserve(chosen.id, request.requirements)) {
                excluded.add(chosen.id)
                continue
            }
            return PlacementDecision.Assigned(
                nodeId = chosen.id,
                strategy = STRATEGY,
                reason = "least-allocated: ${chosen.id} free=$freeBefore",
            )
        }
    }

    companion object {
        const val STRATEGY: String = "least-allocated"
    }
}
