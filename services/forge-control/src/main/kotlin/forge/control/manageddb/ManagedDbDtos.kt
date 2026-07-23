package forge.control.manageddb

import kotlinx.serialization.Serializable

@Serializable
data class CreateDbInstanceRequest(
    val name: String? = null,
    val projectId: String? = null,
)

@Serializable
data class DbInstanceResponse(
    val id: String,
    val projectId: String,
    val name: String,
    val status: String,
    val engine: String,
    val deletionProtection: Boolean,
    val statusReason: String? = null,
    val endpointRef: String? = null,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class DbDatabaseResponse(
    val id: String,
    val instanceId: String,
    val name: String,
    val createdAt: String,
)

fun DbInstance.toResponse(): DbInstanceResponse =
    DbInstanceResponse(
        id = id.toString(),
        projectId = projectId.toString(),
        name = name,
        status = status.wire,
        engine = engine,
        deletionProtection = deletionProtection,
        statusReason = statusReason,
        endpointRef = endpointRef,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )

fun DbDatabase.toResponse(): DbDatabaseResponse =
    DbDatabaseResponse(
        id = id.toString(),
        instanceId = instanceId.toString(),
        name = name,
        createdAt = createdAt.toString(),
    )
