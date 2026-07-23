package forge.control.scheduler

import forge.control.scheduler.model.NodeTaint
import forge.control.scheduler.model.TaintEffect
import forge.control.scheduler.model.Toleration
import forge.control.scheduler.model.UnschedulableReasonEntry

/**
 * Eliminate nodes whose taints are not tolerated by the request.
 * Both [TaintEffect.NoSchedule] and [TaintEffect.NoExecute] block new placements.
 */
object TaintTolerationFilter {
    const val REASON: String = "TaintNotTolerated"

    data class Result(
        val candidates: List<FleetNode>,
        val eliminated: List<UnschedulableReasonEntry>,
    )

    fun filter(
        nodes: List<FleetNode>,
        tolerations: List<Toleration>,
    ): Result {
        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        val kept = mutableListOf<FleetNode>()
        for (node in nodes) {
            val untolerated = untoleratedTaints(node.taints, tolerations)
            if (untolerated.isEmpty()) {
                kept += node
            } else {
                val t = untolerated.first()
                eliminated += UnschedulableReasonEntry(
                    nodeId = node.id,
                    reason = REASON,
                    detail = "taint ${t.key}=${t.value ?: ""}:${t.effect} not tolerated",
                )
            }
        }
        return Result(candidates = kept, eliminated = eliminated)
    }

    fun untoleratedTaints(
        taints: List<NodeTaint>,
        tolerations: List<Toleration>,
    ): List<NodeTaint> =
        taints.filter { taint ->
            val effect = runCatching { taint.effectEnum() }.getOrNull() ?: return@filter true
            if (effect != TaintEffect.NoSchedule && effect != TaintEffect.NoExecute) {
                return@filter false
            }
            tolerations.none { it.matches(taint) }
        }

    fun toleratesAll(
        taints: List<NodeTaint>,
        tolerations: List<Toleration>,
    ): Boolean = untoleratedTaints(taints, tolerations).isEmpty()
}
