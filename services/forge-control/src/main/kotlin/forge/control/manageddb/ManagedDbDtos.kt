package forge.control.manageddb

import kotlinx.serialization.Serializable

@Serializable
data class CreateDbInstanceRequest(
    val name: String? = null,
    val projectId: String? = null,
)

@Serializable
data class CreateDbDatabaseRequest(
    val name: String? = null,
)

@Serializable
data class AttachDatabaseRequest(
    val applicationId: String? = null,
    val envVar: String? = null,
)

@Serializable
data class DbAttachmentResponse(
    val id: String,
    val databaseId: String,
    val applicationId: String,
    val envVar: String,
    val secretRef: String? = null,
    val createdAt: String,
)

fun DbAttachment.toResponse(): DbAttachmentResponse =
    DbAttachmentResponse(
        id = id.toString(),
        databaseId = databaseId.toString(),
        applicationId = applicationId.toString(),
        envVar = envVar,
        secretRef = secretRef,
        createdAt = createdAt.toString(),
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
    val host: String? = null,
    val port: Int? = null,
    val containerId: String? = null,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class DbDatabaseResponse(
    val id: String,
    val instanceId: String,
    val name: String,
    val status: String,
    val statusReason: String? = null,
    val host: String? = null,
    val port: Int? = null,
    val secretRef: String? = null,
    val username: String? = null,
    /** Present only on create (one-time reveal); never in list/get. */
    val password: String? = null,
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
        host = host,
        port = port,
        containerId = containerId,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )

fun DbDatabase.toResponse(
    host: String? = null,
    port: Int? = null,
    secretRef: String? = null,
    username: String? = null,
    password: String? = null,
): DbDatabaseResponse =
    DbDatabaseResponse(
        id = id.toString(),
        instanceId = instanceId.toString(),
        name = name,
        status = status.wire,
        statusReason = statusReason,
        host = host,
        port = port,
        secretRef = secretRef,
        username = username,
        password = password,
        createdAt = createdAt.toString(),
    )
