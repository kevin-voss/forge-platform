package forge.control.resource

import kotlinx.serialization.json.JsonObject

/**
 * Kubernetes-style resource envelope reused by every kind-owning epic.
 * [spec] and [status] are opaque JSON objects interpreted by the owning controller.
 */
data class ResourceEnvelope(
    val apiVersion: String,
    val kind: String,
    val metadata: ResourceMetadata,
    val spec: JsonObject = JsonObject(emptyMap()),
    val status: JsonObject = JsonObject(emptyMap()),
)
