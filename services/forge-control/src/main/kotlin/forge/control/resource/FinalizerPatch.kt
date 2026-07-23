package forge.control.resource

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive

@Serializable
data class FinalizerPatchRequest(
    val add: List<String> = emptyList(),
    val remove: List<String> = emptyList(),
)

object Finalizers {
    fun applyPatch(current: JsonArray, patch: FinalizerPatchRequest): JsonArray {
        val ordered = linkedSetOf<String>()
        for (element in current) {
            val value = (element as? JsonPrimitive)?.content?.trim().orEmpty()
            if (value.isNotEmpty()) ordered.add(value)
        }
        for (name in patch.add) {
            val trimmed = name.trim()
            if (trimmed.isNotEmpty()) ordered.add(trimmed)
        }
        for (name in patch.remove) {
            ordered.remove(name.trim())
        }
        return JsonArray(ordered.map { JsonPrimitive(it) })
    }

    fun asStrings(finalizers: JsonArray): List<String> =
        finalizers.mapNotNull { (it as? JsonPrimitive)?.content?.trim()?.takeIf { s -> s.isNotEmpty() } }

    fun isEmpty(finalizers: JsonArray): Boolean = asStrings(finalizers).isEmpty()
}
