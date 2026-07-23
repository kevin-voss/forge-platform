package forge.control.manageddb

import forge.control.http.ApiException
import forge.control.http.idempotentCreate
import forge.control.http.requireUuid
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.application.ApplicationCall
import io.ktor.server.request.header
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.delete
import io.ktor.server.routing.get
import io.ktor.server.routing.patch
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.json.Json
import java.util.UUID

private const val PROJECT_HEADER = "X-Forge-Project"

fun Route.managedDbRoutes(managedDb: ManagedDbService, idempotency: IdempotencyStore? = null) {
    route("/v1/databases/instances") {
        post {
            val body = call.receive<CreateDbInstanceRequest>()
            val projectId = call.resolveProjectId(body.projectId)
            call.idempotentCreate(
                idempotency,
                "db_instance",
                Json.encodeToString(CreateDbInstanceRequest.serializer(), body),
            ) {
                val created = managedDb.createInstance(projectId, body.name)
                created.id to Json.encodeToJsonElement(
                    DbInstanceResponse.serializer(),
                    created.toResponse(),
                )
            }
        }
        get {
            val projectId = call.resolveProjectId(call.request.queryParameters["projectId"])
            call.respond(managedDb.listInstances(projectId).map { it.toResponse() })
        }
    }

    get("/v1/databases/instances/{instanceId}") {
        val instanceId = call.parameters.requireUuid("instanceId")
        call.respond(managedDb.getInstance(instanceId).toResponse())
    }

    patch("/v1/databases/instances/{instanceId}") {
        val instanceId = call.parameters.requireUuid("instanceId")
        val body = call.receive<PatchDeletionProtectionRequest>()
        call.respond(
            managedDb.patchInstanceDeletionProtection(instanceId, body.deletionProtection).toResponse(),
        )
    }

    delete("/v1/databases/instances/{instanceId}") {
        val instanceId = call.parameters.requireUuid("instanceId")
        val force = call.request.queryParameters["force"]?.equals("true", ignoreCase = true) == true
        managedDb.deleteInstance(instanceId, force = force)
        call.respond(HttpStatusCode.NoContent)
    }

    route("/v1/databases/instances/{instanceId}/databases") {
        get {
            val instanceId = call.parameters.requireUuid("instanceId")
            call.respond(managedDb.listDatabases(instanceId))
        }
        post {
            val instanceId = call.parameters.requireUuid("instanceId")
            val body = call.receive<CreateDbDatabaseRequest>()
            call.idempotentCreate(
                idempotency,
                "db_database",
                Json.encodeToString(CreateDbDatabaseRequest.serializer(), body) + "|$instanceId",
            ) {
                val created = managedDb.createDatabase(instanceId, body.name)
                created.database.id to Json.encodeToJsonElement(
                    DbDatabaseResponse.serializer(),
                    managedDb.toCreateResponse(created),
                )
            }
        }
    }

    get("/v1/databases/{databaseId}") {
        val databaseId = call.parameters.requireUuid("databaseId")
        call.respond(managedDb.getDatabase(databaseId))
    }

    patch("/v1/databases/{databaseId}") {
        val databaseId = call.parameters.requireUuid("databaseId")
        val body = call.receive<PatchDeletionProtectionRequest>()
        call.respond(
            managedDb.patchDatabaseDeletionProtection(databaseId, body.deletionProtection),
        )
    }

    delete("/v1/databases/{databaseId}") {
        val databaseId = call.parameters.requireUuid("databaseId")
        val force = call.request.queryParameters["force"]?.equals("true", ignoreCase = true) == true
        managedDb.deleteDatabase(databaseId, force = force)
        call.respond(HttpStatusCode.NoContent)
    }

    post("/v1/databases/{databaseId}/rotate-credentials") {
        val databaseId = call.parameters.requireUuid("databaseId")
        call.respond(managedDb.rotateCredentials(databaseId))
    }

    post("/v1/databases/{databaseId}/attach") {
        val databaseId = call.parameters.requireUuid("databaseId")
        val body = call.receive<AttachDatabaseRequest>()
        call.idempotentCreate(
            idempotency,
            "db_attachment",
            Json.encodeToString(AttachDatabaseRequest.serializer(), body) + "|$databaseId",
        ) {
            val created = managedDb.attach(databaseId, body.applicationId, body.envVar)
            created.id to Json.encodeToJsonElement(
                DbAttachmentResponse.serializer(),
                created.toResponse(),
            )
        }
    }

    delete("/v1/databases/attachments/{attachmentId}") {
        val attachmentId = call.parameters.requireUuid("attachmentId")
        managedDb.detach(attachmentId)
        call.respond(HttpStatusCode.NoContent)
    }

    get("/v1/applications/{applicationId}/databases") {
        val applicationId = call.parameters.requireUuid("applicationId")
        call.respond(managedDb.listAttachmentsForApplication(applicationId))
    }

    backupRoutes(managedDb, idempotency)
}

private fun ApplicationCall.resolveProjectId(explicit: String?): UUID {
    val raw = explicit?.trim()?.takeIf { it.isNotEmpty() }
        ?: request.header(PROJECT_HEADER)?.trim()?.takeIf { it.isNotEmpty() }
        ?: throw ApiException.BadRequest(
            "projectId is required (body/query projectId or X-Forge-Project header)",
            mapOf("field" to "projectId"),
        )
    return try {
        UUID.fromString(raw)
    } catch (_: IllegalArgumentException) {
        throw ApiException.BadRequest(
            "invalid UUID for projectId",
            mapOf("field" to "projectId"),
        )
    }
}
