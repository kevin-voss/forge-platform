package forge.control.scheduler

import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.NodeTopology
import forge.control.scheduler.model.PlacementDecision
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.PlacementTrace
import forge.control.scheduler.model.PlacementTraceScore
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
    private val workloadAffinity: WorkloadAffinityFilter = WorkloadAffinityFilter.noop(),
    private val topologySpread: TopologySpreadFilter = TopologySpreadFilter.noop(),
    private val placedReplicas: () -> List<Placement> = { emptyList() },
    private val statefulFilter: StatefulPlacementFilter = StatefulPlacementFilter.noop(),
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
            workloadAffinity = workloadAffinity,
            topologySpread = topologySpread,
            placedReplicas = placedReplicas,
            statefulFilter = statefulFilter,
            scoreBase = { 0.0 },
            useScorePick = false,
        )

    companion object {
        const val STRATEGY: String = "first-fit"
    }
}

/** Shared capacity + affinity + topology-spread placement loop for strategy schedulers. */
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
        workloadAffinity: WorkloadAffinityFilter = WorkloadAffinityFilter.noop(),
        topologySpread: TopologySpreadFilter = TopologySpreadFilter.noop(),
        placedReplicas: () -> List<Placement> = { emptyList() },
        statefulFilter: StatefulPlacementFilter = StatefulPlacementFilter.noop(),
        scoreBase: (FleetNode) -> Double = { PlacementCapacity.freeSlots(it).toDouble() },
        useScorePick: Boolean = true,
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

            val affinity = request.placement.affinity
            val requiredTerms = AntiAffinityFilter.expandRequired(
                antiAffinity = request.antiAffinity,
                serviceId = request.serviceId,
                explicit = affinity?.requiredTerms().orEmpty(),
            )
            val preferredTerms = AntiAffinityFilter.expandPreferred(
                antiAffinity = request.antiAffinity,
                serviceId = request.serviceId,
                explicit = affinity?.preferredTerms().orEmpty(),
            )

            val affinityResult = workloadAffinity.filter(taintResult.candidates, requiredTerms)
            trace = trace.withFilter("workload_affinity", affinityResult.eliminated)

            val spreadResult = topologySpread.filter(
                candidates = affinityResult.candidates,
                serviceId = request.serviceId,
                constraints = request.placement.topologySpreadConstraints,
                hardOnly = true,
            )
            trace = trace
                .withFilter("topology_spread", spreadResult.eliminated)
                .withSpreadRelaxed(spreadResult.spreadRelaxed)

            val statefulResult = statefulFilter.filter(
                candidates = spreadResult.candidates,
                deploymentId = request.deploymentId,
                serviceId = request.serviceId,
                stateful = request.placement.stateful,
            )
            trace = trace.withFilter("stateful", statefulResult.eliminated)

            val filtered = statefulResult.candidates
            Span.current().setAttribute(
                AttributeKey.longKey("candidates"),
                filtered.size.toLong(),
            )
            Span.current().setAttribute(AttributeKey.stringKey("strategy"), strategy)
            Span.current().setAttribute(
                AttributeKey.stringArrayKey("filters_applied"),
                trace.filterNames(),
            )
            val topologyKeys = request.placement.topologySpreadConstraints
                .map { it.topologyKey }
                .distinct()
            if (topologyKeys.isNotEmpty()) {
                Span.current().setAttribute(
                    AttributeKey.stringArrayKey("topology_keys_evaluated"),
                    topologyKeys,
                )
            }
            resolved.cpuMillis?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_cpu_millis"), it.toLong())
            }
            resolved.memoryMb?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_memory_mb"), it.toLong())
            }
            request.requirements.gpu?.count?.let {
                Span.current().setAttribute(AttributeKey.longKey("requested_gpu_count"), it.toLong())
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
                    affinityResult.candidates.isEmpty() && requiredTerms.isNotEmpty() ->
                        if (request.antiAffinity == AntiAffinity.Hard &&
                            (affinity == null || affinity.isEmpty())
                        ) {
                            "anti-affinity: no distinct node for service"
                        } else {
                            WorkloadAffinityFilter.REASON
                        }
                    spreadResult.candidates.isEmpty() &&
                        request.placement.topologySpreadConstraints.isNotEmpty() ->
                        TopologySpreadFilter.REASON
                    statefulResult.candidates.isEmpty() &&
                        request.placement.stateful != null &&
                        !request.placement.stateful.isEmpty() ->
                        StatefulPlacementFilter.REASON
                    else -> UnschedulableReasons.summarize(
                        capacityEliminated +
                            selectorResult.eliminated +
                            platformResult.eliminated +
                            taintResult.eliminated +
                            affinityResult.eliminated +
                            spreadResult.eliminated +
                            statefulResult.eliminated,
                    )
                }
                val allEliminated = capacityEliminated +
                    selectorResult.eliminated +
                    platformResult.eliminated +
                    taintResult.eliminated +
                    affinityResult.eliminated +
                    spreadResult.eliminated +
                    statefulResult.eliminated
                return PlacementDecision.NoNodeAvailable(
                    reason = reason,
                    unschedulableReasons = allEliminated,
                    trace = trace,
                )
            }

            // Preserve 08.04 soft anti-affinity narrowing when no explicit topology fields.
            val legacySoftOnly =
                request.antiAffinity == AntiAffinity.Soft &&
                    (affinity == null || affinity.isEmpty()) &&
                    request.placement.topologySpreadConstraints.isEmpty() &&
                    (request.placement.stateful == null || request.placement.stateful.isEmpty())
            val preferredNodes = if (legacySoftOnly) {
                antiAffinity.filterPreferred(request.serviceId, filtered)
            } else {
                filtered
            }
            val candidates = when {
                preferredNodes.isNotEmpty() -> preferredNodes
                legacySoftOnly -> {
                    if (!softFallbackUsed) {
                        onSoftFallback?.invoke()
                        softFallbackUsed = true
                    }
                    filtered
                }
                else -> filtered
            }

            val placedAll = placedReplicas()
            val scores = candidates.map { node ->
                val base = scoreBase(node)
                val spread = SpreadScorer.score(
                    node = node,
                    serviceId = request.serviceId,
                    constraints = request.placement.topologySpreadConstraints,
                    nodesById = nodes::find,
                    placedForService = { sid -> placedAll.filter { it.serviceId == sid } },
                )
                val soft = SoftAffinityScorer.score(
                    node = node,
                    preferred = preferredTerms,
                    nodesById = nodes::find,
                    placed = { placedAll },
                )
                val statefulScore = statefulFilter.score(node, request.placement.stateful)
                val total = base + spread + soft + statefulScore
                PlacementTraceScore(
                    nodeId = node.id,
                    score = total,
                    detail = "base=$base spread=$spread soft=$soft stateful=$statefulScore",
                )
            }
            if (scores.isNotEmpty()) {
                trace = trace.withScores(scores.sortedBy { it.nodeId })
            }

            val scorePick = useScorePick ||
                request.placement.topologySpreadConstraints.isNotEmpty() ||
                preferredTerms.isNotEmpty() ||
                (request.placement.stateful != null && !request.placement.stateful.isEmpty())
            val chosen = if (scorePick) {
                val winner = scores
                    .sortedWith(
                        compareByDescending<PlacementTraceScore> { it.score }
                            .thenBy { it.nodeId },
                    )
                    .first()
                candidates.first { it.id == winner.nodeId }
            } else {
                pick(candidates)
            }

            val freeBefore = PlacementCapacity.freeSlots(chosen)
            if (!reservation.tryReserve(chosen.id, reserveReqs)) {
                excluded.add(chosen.id)
                continue
            }
            Span.current().setAttribute(AttributeKey.stringKey("node"), chosen.id)
            if (!request.serviceId.isNullOrBlank()) {
                val servicePlaced = placedAll.filter { it.serviceId == request.serviceId }
                val nodeIds = (servicePlaced.mapNotNull { it.nodeId } + chosen.id).toSet()
                val zones = nodeIds.mapNotNull { nodes.find(it)?.zone }.toSet()
                Span.current().setAttribute(
                    AttributeKey.longKey("distinct_nodes_used"),
                    nodeIds.size.toLong(),
                )
                Span.current().setAttribute(
                    AttributeKey.longKey("distinct_zones_used"),
                    zones.size.toLong(),
                )
            }
            val volumeRef = request.placement.stateful?.resolvedVolumeRef()
            statefulFilter.recordPlacement(
                deploymentId = request.deploymentId,
                volumeRef = volumeRef,
                selectedNode = chosen.id,
                reason = reasonFor(chosen, freeBefore),
            )
            return PlacementDecision.Assigned(
                nodeId = chosen.id,
                strategy = strategy,
                reason = reasonFor(chosen, freeBefore),
                trace = trace,
                topology = NodeTopology(
                    node = chosen.id,
                    zone = chosen.zone,
                    region = chosen.region,
                    provider = chosen.provider,
                ),
            )
        }
    }
}
