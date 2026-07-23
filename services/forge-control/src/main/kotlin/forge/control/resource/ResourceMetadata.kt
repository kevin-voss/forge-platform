package forge.control.resource

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import java.time.Instant

/**
 * Shared metadata envelope for every declarative resource.
 * [deletionTimestamp] marks terminating-but-visible; [deletedAt] is terminal soft-delete.
 */
data class ResourceMetadata(
    val id: String,
    val name: String,
    val organization: String,
    val project: String? = null,
    val environment: String? = null,
    val generation: Long = 1,
    val resourceVersion: Long,
    val labels: JsonObject = JsonObject(emptyMap()),
    val annotations: JsonObject = JsonObject(emptyMap()),
    val ownerRefs: JsonArray = JsonArray(emptyList()),
    val finalizers: JsonArray = JsonArray(emptyList()),
    val createdAt: Instant,
    val updatedAt: Instant,
    val deletionTimestamp: Instant? = null,
    val deletedAt: Instant? = null,
)
