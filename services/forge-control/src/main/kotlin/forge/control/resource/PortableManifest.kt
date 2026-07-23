package forge.control.resource

import forge.control.http.ApiException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull

/**
 * Rejects provider-specific fields in portable product manifests (step 20.07).
 * Infrastructure detail belongs on operator-owned kinds (NodePool, etc.), never on
 * Application / Service product specs submitted via [forge apply].
 */
object PortableManifest {
    private val forbiddenKeyFragments = listOf(
        "provider",
        "machineType",
        "machine_type",
        "instanceType",
        "instance_type",
        "region",
        "zone",
        "availabilityZone",
        "availability_zone",
        "nodeName",
        "node_name",
        "credential",
        "connectionString",
        "connection_string",
        "securityGroup",
        "security_group",
        "diskType",
        "disk_type",
        "volumeType",
        "volume_type",
    )

    private val forbiddenKeyExact = setOf(
        "aws",
        "azure",
        "hetzner",
        "gcp",
        "cidr",
        "ip",
        "ips",
    )

    private val forbiddenValueTokens = listOf(
        "t3.",
        "m5.",
        "cx41",
        "cx22",
        "Standard_",
        "gp3",
        "Premium_LRS",
        "rds.",
        "amazonaws.com",
    )

    fun validate(kind: String, spec: JsonObject, pathPrefix: String = "spec") {
        walk(spec, pathPrefix) { path, key, element ->
            val normalized = key.trim()
            if (normalized in forbiddenKeyExact ||
                forbiddenKeyFragments.any { fragment ->
                    normalized.contains(fragment, ignoreCase = true)
                }
            ) {
                throw ApiException.BadRequest(
                    "portable manifest must not contain provider-specific field '$path'",
                    details = mapOf(
                        "kind" to kind,
                        "field" to path,
                    ),
                    code = "portable_manifest_violation",
                )
            }
            if (element is JsonPrimitive && element.isString) {
                val value = element.contentOrNull.orEmpty().lowercase()
                for (token in forbiddenValueTokens) {
                    if (value.contains(token.lowercase())) {
                        throw ApiException.BadRequest(
                            "portable manifest must not contain provider-specific value at '$path'",
                            details = mapOf(
                                "kind" to kind,
                                "field" to path,
                            ),
                            code = "portable_manifest_violation",
                        )
                    }
                }
            }
        }
    }

    private fun walk(
        element: JsonElement,
        path: String,
        visitor: (path: String, key: String, element: JsonElement) -> Unit,
    ) {
        when (element) {
            is JsonObject -> {
                for ((key, child) in element) {
                    val childPath = if (path.isEmpty()) key else "$path.$key"
                    visitor(childPath, key, child)
                    walk(child, childPath, visitor)
                }
            }
            is JsonArray -> {
                element.forEachIndexed { index, child ->
                    walk(child, "$path[$index]", visitor)
                }
            }
            else -> Unit
        }
    }
}
