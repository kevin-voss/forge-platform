package forge.control.scheduler

import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span

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
            Span.current().setAttribute(AttributeKey.longKey("candidates"), candidates.size.toLong())
            Span.current().setAttribute(AttributeKey.stringKey("strategy"), STRATEGY)
            if (candidates.isEmpty()) {
                return PlacementDecision.NoNodeAvailable(
                    reason = if (excluded.isEmpty()) {
                        "no node available"
                    } else {
                        "no node available after reservation retries"
                    },
                )
            }
            // Most free slots; deterministic tie-break by lowest node id.
            val chosen = candidates.minWith(
                compareByDescending<FleetNode> { PlacementCapacity.freeSlots(it) }
                    .thenBy { it.id },
            )
            val freeBefore = PlacementCapacity.freeSlots(chosen)
            if (!reservation.tryReserve(chosen.id, request.requirements)) {
                excluded.add(chosen.id)
                continue
            }
            Span.current().setAttribute(AttributeKey.stringKey("node"), chosen.id)
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
