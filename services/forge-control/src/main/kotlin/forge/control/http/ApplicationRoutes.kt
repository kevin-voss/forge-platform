package forge.control.http

import forge.control.http.dto.CreateApplicationRequest
import forge.control.http.dto.toResponse
import forge.control.service.ApplicationService
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.json.Json

fun Route.applicationRoutes(applications: ApplicationService, idempotency: IdempotencyStore? = null) {
    route("/v1/projects/{projectId}/applications") {
        post {
            val projectId = call.parameters.requireUuid("projectId")
            val body = call.receive<CreateApplicationRequest>()
            call.idempotentCreate(idempotency, "application", Json.encodeToString(CreateApplicationRequest.serializer(), body)) {
                val created = applications.create(projectId, body.name)
                created.id to Json.encodeToJsonElement(forge.control.http.dto.ApplicationResponse.serializer(), created.toResponse())
            }
        }
        get {
            val projectId = call.parameters.requireUuid("projectId")
            call.respond(applications.list(projectId).map { it.toResponse() })
        }
    }
    get("/v1/applications/{applicationId}") {
        val applicationId = call.parameters.requireUuid("applicationId")
        call.respond(applications.get(applicationId).toResponse())
    }
}
