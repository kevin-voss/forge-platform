package forge.control.scheduler

import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementTrace
import forge.control.scheduler.model.UnschedulableReasons
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span

/**
 * Place on the first online node (stable id order) with enough free capacity,
 * preferring nodes without a same-service replica when anti-affinity applies.
 */
class FirstFitScheduler(
    private val nodes: NodeStore,
    private val reservation: CapacityReservation,
    private val antiAffinity: AntiAffinityFilter = AntiAffinityFilter.noop(),
    private val onSoftFallback: (() -> Unit)? = null,
) : Scheduler {
    override fun place(request: PlacementRequest): PlacementDecision =
        CapacityAwarePlacement.place(
            nodes = nodes,
            reservation = reservation,
            request = request,
            strategy = STRATEGY,
            antiAffinity = antiAffinity,
            onSoftFallback = onSoftFallback,
            pick = { candidates -> candidates.first() },
            reasonFor = { chosen, freeBefore -> "first-fit: ${chosen.id} free=$freeBefore" },
        )

    companion object {
        const val STRATEGY: String = "first-fit"
    }
}

/** Shared capacity + anti-affinity placement loop for strategy schedulers. */
internal object CapacityAwarePlacement {
    fun place(
        nodes: NodeStore,
        reservation: CapacityReservation,
        request: PlacementRequest,
        strategy: String,
        antiAffinity: AntiAffinityFilter,
        onSoftFallback: (() -> Unit)?,
        pick: (List<FleetNode>) -> FleetNode,
        reasonFor: (FleetNode, Int) -> String,
    ): PlacementDecision {
        val resolved = RequirementsResolver.resolve(request.requirements)
        val reserveReqs = resolved.toResourceRequirements()
        val excluded = linkedSetOf<String>()
        var softFallbackUsed = false
        var lastEliminated = PlacementCapacity.eliminated(nodes, request.requirements, excluded)
        while (true) {
            val capacityCandidates = PlacementCapacity.candidates(
                nodes,
                request.requirements,
                excluded,
            )
            lastEliminated = PlacementCapacity.eliminated(nodes, request.requirements, excluded)
            Span.current().setAttribute(
                AttributeKey.longKey("candidates"),
                capacityCandidates.size.toLong(),
            )
            Span.current().setAttribute(AttributeKey.stringKey("strategy"), strategy)
            resolved.cpuMillis?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_cpu_millis"), it.toLong())
            }
            resolved.memoryMb?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_memory_mb"), it.toLong())
            }
            if (capacityCandidates.isEmpty()) {
                val reason = when {
                    excluded.isNotEmpty() -> "no node available after reservation retries"
                    lastEliminated.isNotEmpty() -> UnschedulableReasons.summarize(lastEliminated)
                    else -> "no node with ${resolved.slots} free slot" +
                        if (resolved.slots == 1) "" else "s"
                }
                val trace = PlacementTrace()
                    .withStrategy(strategy)
                    .withCapacityFilter(lastEliminated)
                return PlacementDecision.NoNodeAvailable(
                    reason = reason,
                    unschedulableReasons = lastEliminated,
                    trace = trace,
                )
            }

            val preferred = antiAffinity.filterPreferred(request.serviceId, capacityCandidates)
            val candidates = when {
                preferred.isNotEmpty() -> preferred
                request.antiAffinity == AntiAffinity.Hard -> {
                    val trace = PlacementTrace()
                        .withStrategy(strategy)
                        .withCapacityFilter(lastEliminated)
                    return PlacementDecision.NoNodeAvailable(
                        reason = "anti-affinity: no distinct node for service",
                        unschedulableReasons = lastEliminated,
                        trace = trace,
                    )
                }
                else -> {
                    if (!softFallbackUsed) {
                        onSoftFallback?.invoke()
                        softFallbackUsed = true
                    }
                    capacityCandidates
                }
            }

            val chosen = pick(candidates)
            val freeBefore = PlacementCapacity.freeSlots(chosen)
            if (!reservation.tryReserve(chosen.id, reserveReqs)) {
                excluded.add(chosen.id)
                continue
            }
            Span.current().setAttribute(AttributeKey.stringKey("node"), chosen.id)
            val trace = PlacementTrace()
                .withStrategy(strategy)
                .withCapacityFilter(lastEliminated)
            return PlacementDecision.Assigned(
                nodeId = chosen.id,
                strategy = strategy,
                reason = reasonFor(chosen, freeBefore),
                trace = trace,
            )
        }
    }
}
