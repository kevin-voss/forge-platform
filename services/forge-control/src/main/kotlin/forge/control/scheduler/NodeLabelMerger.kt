package forge.control.scheduler

/**
 * Combines reserved (forge.dev/…) + pool + agent-reported labels.
 * Pool vs agent: agent (node-level) wins on key conflict.
 * Reserved labels always win for forge.dev/ keys.
 */
object NodeLabelMerger {
    const val LABEL_NODE_ID: String = "forge.dev/node-id"
    const val LABEL_ARCH: String = "forge.dev/arch"
    const val LABEL_OS: String = "forge.dev/os"
    const val LABEL_PROVIDER: String = "forge.dev/provider"

    data class MergeResult(
        val labels: Map<String, String>,
        val conflicts: List<LabelConflict> = emptyList(),
    )

    data class LabelConflict(
        val key: String,
        val poolValue: String,
        val nodeValue: String,
    )

    fun merge(
        nodeId: String,
        architecture: String,
        os: String,
        provider: String,
        poolLabels: Map<String, String> = emptyMap(),
        agentLabels: Map<String, String> = emptyMap(),
    ): MergeResult {
        val conflicts = mutableListOf<LabelConflict>()
        val merged = linkedMapOf<String, String>()
        for ((k, v) in poolLabels) {
            if (k.isBlank()) continue
            merged[k] = v
        }
        for ((k, v) in agentLabels) {
            if (k.isBlank()) continue
            val prior = merged[k]
            if (prior != null && prior != v) {
                conflicts += LabelConflict(key = k, poolValue = prior, nodeValue = v)
            }
            merged[k] = v
        }
        // Reserved facts always win.
        merged[LABEL_NODE_ID] = nodeId
        merged[LABEL_ARCH] = architecture
        merged[LABEL_OS] = os
        merged[LABEL_PROVIDER] = provider.ifBlank { "unknown" }
        return MergeResult(labels = merged.toMap(), conflicts = conflicts)
    }
}
