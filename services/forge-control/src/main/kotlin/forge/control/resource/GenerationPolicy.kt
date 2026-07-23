package forge.control.resource

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Decides whether a write bumps [ResourceMetadata.generation].
 *
 * Generation increments only when [spec] changes after canonical (key-sorted) JSON
 * comparison. Label/annotation/finalizer/status-only writes never bump generation.
 */
object GenerationPolicy {
    /** True when [nextSpec] is not byte-equal to [previousSpec] after canonicalization. */
    fun shouldBumpGeneration(previousSpec: JsonObject, nextSpec: JsonObject): Boolean =
        canonicalize(previousSpec) != canonicalize(nextSpec)

    /**
     * Canonical JSON string: object keys sorted recursively so key-order differences
     * do not count as a spec change.
     */
    fun canonicalize(element: JsonElement): String =
        when (element) {
            is JsonObject -> {
                val parts = element.keys.sorted().joinToString(",") { key ->
                    val encodedKey = JsonPrimitive(key).toString()
                    "$encodedKey:${canonicalize(element.getValue(key))}"
                }
                "{$parts}"
            }
            is JsonArray -> {
                val parts = element.joinToString(",") { canonicalize(it) }
                "[$parts]"
            }
            is JsonPrimitive -> element.toString()
            JsonNull -> "null"
        }
}
