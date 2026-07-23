package forge.control.resource

import forge.control.http.ApiException

/**
 * Optimistic concurrency helper for resource writes.
 * Create (no prior version) always passes; mismatch raises typed conflict.
 */
object ResourceVersionGuard {
    fun checkMatch(expected: Long, current: Long) {
        if (expected != current) {
            throw conflict(expected, current)
        }
    }

    /** Create path: no stored version yet. */
    fun acceptCreate() {
        // no-op: documents the create case for callers/tests
    }

    fun conflict(expected: Long, current: Long): ApiException =
        ApiException.Conflict(
            message = "resourceVersion $expected is stale",
            details = mapOf("currentResourceVersion" to current.toString()),
            code = "resource_version_conflict",
        )
}
