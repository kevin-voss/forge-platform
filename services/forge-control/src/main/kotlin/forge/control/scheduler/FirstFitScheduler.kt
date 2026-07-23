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
            pick = { candidates -> candidates.first() },
            reasonFor = { chosen, freeBefore -> "first-fit: ${chosen.id} free=$freeBefore" },
            strictNodeSelector = strictNodeSelector,
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
        strictNodeSelector: Boolean = false,
    ): PlacementDecision {
        val resolved = RequirementsResolver.resolve(request.requirements)
        val reserveReqs = resolved.toResourceRequirements()
        val excluded = linkedSetOf<String>()
        var softFallbackUsed = false
        while (true) {
            val capacityCandidates = PlacementCapacity.candidates(
                nodes,
                request.requirements,
                excluded,
            )
            val capacityEliminated = PlacementCapacity.eliminated(
                nodes,
                request.requirements,
                excluded,
            )
            var trace = PlacementTrace()
                .withStrategy(strategy)
                .withCapacityFilter(capacityEliminated)

            val selectorResult = NodeSelectorFilter.filter(
                capacityCandidates,
                request.placement.nodeSelector,
                strictEmpty = strictNodeSelector,
            )
            trace = trace.withFilter("node_selector", selectorResult.eliminated)

            val platformResult = PlatformFilter.filter(
                selectorResult.candidates,
                request.platform,
            )
            trace = trace.withFilter("platform", platformResult.eliminated)

            val taintResult = TaintTolerationFilter.filter(
                platformResult.candidates,
                request.placement.tolerations,
            )
            trace = trace.withFilter("taints", taintResult.eliminated)

            val filtered = taintResult.candidates
            Span.current().setAttribute(
                AttributeKey.longKey("candidates"),
                filtered.size.toLong(),
            )
            Span.current().setAttribute(AttributeKey.stringKey("strategy"), strategy)
            Span.current().setAttribute(
                AttributeKey.stringArrayKey("filters_applied"),
                trace.filterNames(),
            )
            resolved.cpuMillis?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_cpu_millis"), it.toLong())
            }
            resolved.memoryMb?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_memory_mb"), it.toLong())
            }

            if (filtered.isEmpty()) {
                val reason = when {
                    excluded.isNotEmpty() -> "no node available after reservation retries"
                    capacityCandidates.isEmpty() && capacityEliminated.isNotEmpty() ->
                        UnschedulableReasons.summarize(capacityEliminated)
                    capacityCandidates.isEmpty() ->
                        "no node with ${resolved.slots} free slot" +
                            if (resolved.slots == 1) "" else "s"
                    selectorResult.candidates.isEmpty() &&
                        request.placement.nodeSelector.isNotEmpty() ->
                        NodeSelectorFilter.REASON
                    platformResult.candidates.isEmpty() &&
                        request.platform != null &&
                        !request.platform.isEmpty() ->
                        PlatformFilter.REASON
                    taintResult.candidates.isEmpty() -> TaintTolerationFilter.REASON
                    else -> UnschedulableReasons.summarize(
                        capacityEliminated +
                            selectorResult.eliminated +
                            platformResult.eliminated +
                            taintResult.eliminated,
                    )
                }
                val allEliminated = capacityEliminated +
                    selectorResult.eliminated +
                    platformResult.eliminated +
                    taintResult.eliminated
                return PlacementDecision.NoNodeAvailable(
                    reason = reason,
                    unschedulableReasons = allEliminated,
                    trace = trace,
                )
            }

            val preferred = antiAffinity.filterPreferred(request.serviceId, filtered)
            val candidates = when {
                preferred.isNotEmpty() -> preferred
                request.antiAffinity == AntiAffinity.Hard -> {
                    return PlacementDecision.NoNodeAvailable(
                        reason = "anti-affinity: no distinct node for service",
                        unschedulableReasons = capacityEliminated,
                        trace = trace,
                    )
                }
                else -> {
                    if (!softFallbackUsed) {
                        onSoftFallback?.invoke()
                        softFallbackUsed = true
                    }
                    filtered
                }
            }

            val chosen = pick(candidates)
            val freeBefore = PlacementCapacity.freeSlots(chosen)
            if (!reservation.tryReserve(chosen.id, reserveReqs)) {
                excluded.add(chosen.id)
                continue
            }
            Span.current().setAttribute(AttributeKey.stringKey("node"), chosen.id)
            return PlacementDecision.Assigned(
                nodeId = chosen.id,
                strategy = strategy,
                reason = reasonFor(chosen, freeBefore),
                trace = trace,
            )
        }
    }
}
