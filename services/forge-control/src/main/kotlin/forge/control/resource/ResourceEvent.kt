package forge.control.resource

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import java.time.Instant

/** Durable resource change types emitted into [control.resource_events]. */
enum class ResourceEventType {
    ADDED,
    MODIFIED,
    STATUS_MODIFIED,
    DELETED,
}

/** Persisted / streamed resource event. */
data class ResourceEvent(
    val resourceVersion: Long,
    val eventId: String,
    val eventType: ResourceEventType,
    val kind: String,
    val organization: String,
    val project: String?,
    val environment: String?,
    val resourceId: String,
    val resourceName: String,
    val generation: Long,
    val payload: JsonObject,
    val actor: String?,
    val requestId: String?,
    val createdAt: Instant,
)

/** Insert payload for a new event row (resource_version already assigned by the mutation). */
data class NewResourceEvent(
    val resourceVersion: Long,
    val eventId: String,
    val eventType: ResourceEventType,
    val kind: String,
    val organization: String,
    val project: String?,
    val environment: String?,
    val resourceId: String,
    val resourceName: String,
    val generation: Long,
    val payload: JsonObject,
    val actor: String? = null,
    val requestId: String? = null,
)

/** SSE / JSON body for a watch frame. */
@Serializable
data class ResourceWatchEvent(
    val type: String,
    val resourceVersion: String,
    val resource: ResourceEnvelopeResponse,
)
