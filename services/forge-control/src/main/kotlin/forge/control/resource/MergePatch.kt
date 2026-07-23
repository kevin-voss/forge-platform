package forge.control.resource

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject

/**
 * RFC 7396 JSON Merge Patch applied over [JsonObject] targets.
 *
 * * `null` removes a key
 * * objects are deep-merged
 * * scalars / arrays replace
 */
object MergePatch {
    fun apply(target: JsonObject, patch: JsonObject): JsonObject =
        mergeObject(target, patch)

    private fun mergeObject(target: JsonObject, patch: JsonObject): JsonObject =
        buildJsonObject {
            for ((key, value) in target) {
                if (!patch.containsKey(key)) {
                    put(key, value)
                }
            }
            for ((key, value) in patch) {
                when {
                    value is JsonNull -> Unit // remove
                    value is JsonObject -> {
                        val existing = target[key]
                        val base = if (existing is JsonObject) existing else JsonObject(emptyMap())
                        put(key, mergeObject(base, value.jsonObject))
                    }
                    else -> put(key, value)
                }
            }
        }
}
