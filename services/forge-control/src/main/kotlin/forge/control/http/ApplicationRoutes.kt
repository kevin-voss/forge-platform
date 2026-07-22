package forge.control.http

import forge.control.http.dto.CreateApplicationRequest
import forge.control.http.dto.toResponse
import forge.control.service.ApplicationService
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.applicationRoutes(applications: ApplicationService) {
    route("/v1/projects/{projectId}/applications") {
        post {
            val projectId = call.parameters.requireUuid("projectId")
            val body = call.receive<CreateApplicationRequest>()
            call.respond(HttpStatusCode.Created, applications.create(projectId, body.name).toResponse())
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
