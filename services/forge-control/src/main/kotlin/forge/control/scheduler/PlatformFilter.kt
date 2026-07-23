package forge.control.scheduler

import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.UnschedulableReasonEntry

/**
 * Match optional [PlatformSpec] against node architecture/OS facts.
 * Omitted/empty platform matches any node.
 */
object PlatformFilter {
    const val REASON: String = "PlatformMismatch"

    data class Result(
        val candidates: List<FleetNode>,
        val eliminated: List<UnschedulableReasonEntry>,
    )

    fun filter(
        nodes: List<FleetNode>,
        platform: PlatformSpec?,
    ): Result {
        if (platform == null || platform.isEmpty()) {
            return Result(candidates = nodes, eliminated = emptyList())
        }
        val wantArch = platform.architecture?.trim()?.takeIf { it.isNotEmpty() }
        val wantOs = platform.os?.trim()?.takeIf { it.isNotEmpty() }
        val eliminated = mutableListOf<UnschedulableReasonEntry>()
        val kept = mutableListOf<FleetNode>()
        for (node in nodes) {
            val archOk = wantArch == null || node.architecture == wantArch
            val osOk = wantOs == null || node.os.equals(wantOs, ignoreCase = true)
            if (archOk && osOk) {
                kept += node
            } else {
                val parts = buildList {
                    if (!archOk) add("architecture want=$wantArch have=${node.architecture}")
                    if (!osOk) add("os want=$wantOs have=${node.os}")
                }
                eliminated += UnschedulableReasonEntry(
                    nodeId = node.id,
                    reason = REASON,
                    detail = parts.joinToString("; "),
                )
            }
        }
        return Result(candidates = kept, eliminated = eliminated)
    }
}
