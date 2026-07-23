package forge.control.resource

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject

@Serializable
data class ResourceWriteRequest(
    val apiVersion: String? = null,
    val kind: String? = null,
    val metadata: ResourceMetadataWrite? = null,
    val spec: JsonObject = JsonObject(emptyMap()),
)

@Serializable
data class ResourceMetadataWrite(
    val name: String? = null,
    val organization: String? = null,
    val project: String? = null,
    val environment: String? = null,
    val labels: JsonObject? = null,
    val annotations: JsonObject? = null,
    val resourceVersion: String? = null,
    val ownerRefs: JsonArray? = null,
    val finalizers: JsonArray? = null,
)

@Serializable
data class ResourceEnvelopeResponse(
    val apiVersion: String,
    val kind: String,
    val metadata: ResourceMetadataResponse,
    val spec: JsonObject,
    val status: JsonObject,
)

@Serializable
data class ResourceMetadataResponse(
    val id: String,
    val name: String,
    val organization: String,
    val project: String? = null,
    val environment: String? = null,
    val generation: Long,
    val resourceVersion: String,
    val labels: JsonObject = JsonObject(emptyMap()),
    val annotations: JsonObject = JsonObject(emptyMap()),
    val ownerRefs: JsonArray = JsonArray(emptyList()),
    val finalizers: JsonArray = JsonArray(emptyList()),
    val createdAt: String,
    val updatedAt: String,
    val deletionTimestamp: String? = null,
    val deletedAt: String? = null,
)

fun ResourceRow.toResponse(): ResourceEnvelopeResponse =
    ResourceEnvelopeResponse(
        apiVersion = apiVersion,
        kind = kind,
        metadata = ResourceMetadataResponse(
            id = id,
            name = name,
            organization = organization,
            project = project,
            environment = environment,
            generation = generation,
            resourceVersion = resourceVersion.toString(),
            labels = labels,
            annotations = annotations,
            ownerRefs = ownerRefs,
            finalizers = finalizers,
            createdAt = createdAt.toString(),
            updatedAt = updatedAt.toString(),
            deletionTimestamp = deletionTimestamp?.toString(),
            deletedAt = deletedAt?.toString(),
        ),
        spec = spec,
        status = status,
    )
