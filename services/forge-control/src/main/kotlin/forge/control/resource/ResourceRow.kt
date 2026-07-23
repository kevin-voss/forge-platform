package forge.control.resource

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import java.time.Instant

private val resourceJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
}

/** Input for a new resource row insert. */
data class NewResourceRow(
    val id: String,
    val kind: String,
    val apiVersion: String = "forge.dev/v1",
    val organization: String,
    val project: String?,
    val environment: String?,
    val name: String,
    val labels: JsonObject = JsonObject(emptyMap()),
    val annotations: JsonObject = JsonObject(emptyMap()),
    val spec: JsonObject,
    val ownerRefs: JsonArray = JsonArray(emptyList()),
    val finalizers: JsonArray = JsonArray(emptyList()),
)

/** Persisted resource row (DB ↔ envelope mapping). */
data class ResourceRow(
    val id: String,
    val kind: String,
    val apiVersion: String,
    val organization: String,
    val project: String?,
    val environment: String?,
    val name: String,
    val generation: Long,
    val resourceVersion: Long,
    val labels: JsonObject,
    val annotations: JsonObject,
    val spec: JsonObject,
    val status: JsonObject,
    val ownerRefs: JsonArray,
    val finalizers: JsonArray,
    val createdAt: Instant,
    val updatedAt: Instant,
    val deletedAt: Instant? = null,
    val deletionTimestamp: Instant? = null,
) {
    fun toEnvelope(): ResourceEnvelope =
        ResourceEnvelope(
            apiVersion = apiVersion,
            kind = kind,
            metadata = ResourceMetadata(
                id = id,
                name = name,
                organization = organization,
                project = project,
                environment = environment,
                generation = generation,
                resourceVersion = resourceVersion,
                labels = labels,
                annotations = annotations,
                ownerRefs = ownerRefs,
                finalizers = finalizers,
                createdAt = createdAt,
                updatedAt = updatedAt,
                deletionTimestamp = deletionTimestamp,
                deletedAt = deletedAt,
            ),
            spec = spec,
            status = status,
        )
}

internal fun parseJsonObject(raw: String): JsonObject =
    resourceJson.parseToJsonElement(raw).jsonObject

internal fun parseJsonArray(raw: String): JsonArray =
    resourceJson.parseToJsonElement(raw).jsonArray

internal fun JsonObject.encode(): String = resourceJson.encodeToString(JsonObject.serializer(), this)

internal fun JsonArray.encode(): String = resourceJson.encodeToString(JsonArray.serializer(), this)
