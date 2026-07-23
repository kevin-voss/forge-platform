package forge.control.scheduler

import forge.control.scheduler.model.AntiAffinity
import forge.control.scheduler.model.PlacementRequest
import forge.control.scheduler.model.ResourceRequirements
import forge.control.telemetry.Telemetry
import io.opentelemetry.api.common.AttributeKey
import io.opentelemetry.api.trace.Span

data class PreemptionSelection(
    val nodeId: String,
    val victims: List<Placement>,
    val preemptorPriority: Int,
    val freedSlots: Int,
    val freedCpuMillis: Int,
    val freedMemoryMb: Int,
)

/**
 * Finds the minimal strictly-lower-priority victim set on a topologically-eligible
 * node that frees enough capacity for [request]. Single-pass only — never chains.
 */
class PreemptionSelector(
    private val nodes: NodeStore,
    private val placements: PlacementStore,
    private val priorityClasses: PriorityClassStore,
    private val workloadAffinity: WorkloadAffinityFilter = WorkloadAffinityFilter.noop(),
    private val topologySpread: TopologySpreadFilter = TopologySpreadFilter.noop(),
    private val strictNodeSelector: Boolean = false,
    private val budgetGuard: DisruptionBudgetGuard? = null,
    private val telemetry: Telemetry = Telemetry.current(),
) {
    fun findMinimalVictims(
        request: PlacementRequest,
        preemptorClass: PriorityClass,
    ): PreemptionSelection? =
        telemetry.inSpan("scheduler.preempt") {
            val resolved = RequirementsResolver.resolve(request.requirements)
            val structural = structuralCandidates(request)
            if (structural.isEmpty()) return@inSpan null

            val placed = placements.listPlaced()
            var best: PreemptionSelection? = null
            for (node in structural) {
                val candidates = placed
                    .filter { it.nodeId == node.id }
                    .filter { priorityOf(it) < preemptorClass.value }
                    .sortedWith(
                        compareBy<Placement> { priorityOf(it) }
                            .thenByDescending { it.slots }
                            .thenBy { it.id },
                    )
                if (candidates.isEmpty()) continue

                val victims = minimalSubset(node, resolved.toResourceRequirements(), candidates)
                    ?: continue
                if (budgetGuard != null) {
                    val blocked = victims.any { v ->
                        !budgetGuard.allowsVoluntaryRemoval(v.deploymentId).allowed
                    }
                    if (blocked) continue
                }

                val selection = PreemptionSelection(
                    nodeId = node.id,
                    victims = victims,
                    preemptorPriority = preemptorClass.value,
                    freedSlots = victims.sumOf { it.slots.coerceAtLeast(1) },
                    freedCpuMillis = victims.sumOf { it.requests?.cpuMillis ?: 0 },
                    freedMemoryMb = victims.sumOf { it.requests?.memoryMb ?: 0 },
                )
                best = pickBetter(best, selection)
            }

            val chosen = best
            if (chosen != null) {
                val span = Span.current()
                span.setAttribute(AttributeKey.longKey("victims_count"), chosen.victims.size.toLong())
                span.setAttribute(AttributeKey.stringKey("node"), chosen.nodeId)
                span.setAttribute(
                    AttributeKey.longKey("freed_cpu_millis"),
                    chosen.freedCpuMillis.toLong(),
                )
                span.setAttribute(
                    AttributeKey.longKey("freed_memory_mb"),
                    chosen.freedMemoryMb.toLong(),
                )
            }
            chosen
        }

    private fun structuralCandidates(request: PlacementRequest): List<FleetNode> {
        val online = nodes.list().filter { it.status == "online" }.sortedBy { it.id }
        val selectorResult = NodeSelectorFilter.filter(
            online,
            request.placement.nodeSelector,
            strictEmpty = strictNodeSelector,
        )
        val platformResult = PlatformFilter.filter(selectorResult.candidates, request.platform)
        val taintResult = TaintTolerationFilter.filter(
            platformResult.candidates,
            request.placement.tolerations,
        )
        val affinity = request.placement.affinity
        val requiredTerms = AntiAffinityFilter.expandRequired(
            antiAffinity = request.antiAffinity,
            serviceId = request.serviceId,
            explicit = affinity?.requiredTerms().orEmpty(),
        )
        val affinityResult = workloadAffinity.filter(taintResult.candidates, requiredTerms)
        val spreadResult = topologySpread.filter(
            candidates = affinityResult.candidates,
            serviceId = request.serviceId,
            constraints = request.placement.topologySpreadConstraints,
            hardOnly = true,
        )
        return spreadResult.candidates
    }

    private fun minimalSubset(
        node: FleetNode,
        requirements: ResourceRequirements,
        candidates: List<Placement>,
    ): List<Placement>? {
        val maxSize = candidates.size.coerceAtMost(MAX_VICTIMS)
        for (size in 1..maxSize) {
            val combos = combinations(candidates, size)
                .sortedWith(
                    compareBy<List<Placement>> { combo -> combo.sumOf { priorityOf(it) } }
                        .thenBy { combo -> combo.sumOf { it.slots } }
                        .thenBy { combo -> combo.joinToString(",") { it.id } },
                )
            for (combo in combos) {
                if (fitsAfterFreeing(node, requirements, combo)) {
                    return combo
                }
            }
        }
        return null
    }

    private fun fitsAfterFreeing(
        node: FleetNode,
        requirements: ResourceRequirements,
        victims: List<Placement>,
    ): Boolean {
        val freedSlots = victims.sumOf { it.slots.coerceAtLeast(1) }
        val freedCpu = victims.sumOf { it.requests?.cpuMillis ?: 0 }
        val freedMem = victims.sumOf { it.requests?.memoryMb ?: 0 }
        val freedDisk = victims.sumOf { it.requests?.diskMb ?: 0 }
        val simulated = node.copy(
            allocation = node.allocation.copy(
                slots = (node.allocation.slots - freedSlots).coerceAtLeast(0),
                cpuMillis = node.allocation.cpuMillis?.let { (it - freedCpu).coerceAtLeast(0) },
                memMb = node.allocation.memMb?.let { (it - freedMem).coerceAtLeast(0) },
                diskMb = node.allocation.diskMb?.let { (it - freedDisk).coerceAtLeast(0) },
            ),
        )
        return PlacementCapacity.fits(simulated, requirements)
    }

    private fun priorityOf(placement: Placement): Int =
        priorityClasses.resolve(placement.priorityClass).value

    private fun pickBetter(
        current: PreemptionSelection?,
        candidate: PreemptionSelection,
    ): PreemptionSelection {
        if (current == null) return candidate
        val bySize = candidate.victims.size.compareTo(current.victims.size)
        if (bySize != 0) return if (bySize < 0) candidate else current
        val candPri = candidate.victims.sumOf { priorityOf(it) }
        val curPri = current.victims.sumOf { priorityOf(it) }
        if (candPri != curPri) return if (candPri < curPri) candidate else current
        return if (candidate.nodeId < current.nodeId) candidate else current
    }

    private fun <T> combinations(items: List<T>, size: Int): List<List<T>> {
        if (size <= 0) return listOf(emptyList())
        if (size > items.size) return emptyList()
        if (size == items.size) return listOf(items)
        val result = mutableListOf<List<T>>()
        fun walk(start: Int, acc: MutableList<T>) {
            if (acc.size == size) {
                result += acc.toList()
                return
            }
            for (i in start until items.size) {
                acc += items[i]
                walk(i + 1, acc)
                acc.removeAt(acc.lastIndex)
            }
        }
        walk(0, mutableListOf())
        return result
    }

    companion object {
        const val MAX_VICTIMS: Int = 8
    }
}
