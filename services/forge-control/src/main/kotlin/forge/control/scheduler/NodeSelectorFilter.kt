package forge.control.scheduler

import forge.control.scheduler.model.UnschedulableReasonEntry

/**
 * Equality-match [placement.nodeSelector] against node labels.
 * Empty selector is a no-op unless [strictEmpty] is true.
 */
object NodeSelectorFilter {
    const val REASON: String = "NoNodeMatchesSelector"

    data class Result(
        val candidates: List<FleetNode>,
        val eliminated: List<UnschedulableReasonEntry>,
    )

    fun filter(
        nodes: List<FleetNode>,
        nodeSelector: Map<String, String>,
        strictEmpty: Boolean = false,
    ): Result {
        if (nodeSelector.isEmpty()) {
            if (strictEmpty) {
                return Result(
                    candidates = emptyList(),
                    eliminated = nodes.map {
                        UnschedulableReasonEntry(
                            nodeId = it.id,
                            reason = REASON,
                            detail = "empty nodeSelector rejected (FORGE_STRICT_NODE_SELECTOR)",
                        )
                    },
                )
            }
            return Result(candidates = nodes, eliminated = emptyList())
        }

        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        val kept = mutableListOf<FleetNode>()
        for (node in nodes) {
            val missing = nodeSelector.entries.firstOrNull { (k, v) ->
                node.labels[k] != v
            }
            if (missing == null) {
                kept += node
            } else {
                eliminated += UnschedulableReasonEntry(
                    nodeId = node.id,
                    reason = REASON,
                    detail = "missing or mismatch label ${missing.key}=${missing.value}" +
                        " (have ${node.labels[missing.key] ?: "<absent>"})",
                )
            }
        }
        return Result(candidates = kept, eliminated = eliminated)
    }
}
