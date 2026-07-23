package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

/**
 * Validates Kubernetes-style label/annotation key and value grammar on write.
 *
 * Keys: optional DNS subdomain prefix (≤253) + `/` + name segment matching
 * `[a-z0-9]([-a-z0-9_.]*[a-z0-9])?` (≤63). Values ≤63 chars.
 */
object LabelValidator {
    private val nameSegment = Regex("^[a-z0-9]([-a-z0-9_.]*[a-z0-9])?$")
    private val dnsSubdomain = Regex(
        "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$",
    )

    fun validate(labels: JsonObject, annotations: JsonObject) {
        validateMap(labels, field = "metadata.labels")
        validateMap(annotations, field = "metadata.annotations")
    }

    fun validateKey(key: String, field: String = "key") {
        if (key.isEmpty()) {
            throw ApiException.BadRequest(
                "label/annotation key must not be empty",
                details = mapOf("field" to field, "key" to key),
                code = "invalid_label",
            )
        }
        val slash = key.indexOf('/')
        val name = if (slash >= 0) {
            val prefix = key.substring(0, slash)
            if (prefix.isEmpty() || prefix.length > 253 || !dnsSubdomain.matches(prefix)) {
                throw ApiException.BadRequest(
                    "label/annotation key prefix is invalid",
                    details = mapOf("field" to field, "key" to key),
                    code = "invalid_label",
                )
            }
            key.substring(slash + 1)
        } else {
            key
        }
        if (name.isEmpty() || name.length > 63 || !nameSegment.matches(name)) {
            throw ApiException.BadRequest(
                "label/annotation key name segment is invalid",
                details = mapOf("field" to field, "key" to key),
                code = "invalid_label",
            )
        }
    }

    fun validateValue(value: String, field: String = "value", key: String = "") {
        if (value.length > 63) {
            throw ApiException.BadRequest(
                "label/annotation value exceeds 63 characters",
                details = buildMap {
                    put("field", field)
                    if (key.isNotEmpty()) put("key", key)
                },
                code = "invalid_label",
            )
        }
        if (value.isNotEmpty() && !nameSegment.matches(value)) {
            throw ApiException.BadRequest(
                "label/annotation value is invalid",
                details = buildMap {
                    put("field", field)
                    if (key.isNotEmpty()) put("key", key)
                },
                code = "invalid_label",
            )
        }
    }

    private fun validateMap(map: JsonObject, field: String) {
        for ((key, element) in map) {
            validateKey(key, field = field)
            val value = when (element) {
                is JsonPrimitive -> element.contentOrNull
                    ?: throw ApiException.BadRequest(
                        "label/annotation values must be strings",
                        details = mapOf("field" to field, "key" to key),
                        code = "invalid_label",
                    )
                else -> throw ApiException.BadRequest(
                    "label/annotation values must be strings",
                    details = mapOf("field" to field, "key" to key),
                    code = "invalid_label",
                )
            }
            validateValue(value, field = field, key = key)
        }
    }
}
