package forge.control.resource

import kotlinx.serialization.Serializable

/**
 * Shared condition type for resource status (Kubernetes-shaped).
 *
 * [status] is `"True"`, `"False"`, or `"Unknown"` (quoted strings, not booleans).
 * [lastTransitionTime] is ISO-8601 UTC; set by [ConditionMerge] when [status] changes.
 */
@Serializable
data class Condition(
    val type: String,
    val status: String,
    val reason: String = "",
    val message: String = "",
    val lastTransitionTime: String? = null,
) {
    init {
        require(type.isNotBlank()) { "condition type must not be blank" }
        require(status in VALID_STATUSES) {
            "condition status must be one of $VALID_STATUSES, got '$status'"
        }
    }

    companion object {
        val VALID_STATUSES = setOf("True", "False", "Unknown")
    }
}
