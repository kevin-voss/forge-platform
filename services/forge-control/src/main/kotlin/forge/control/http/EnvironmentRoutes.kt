package forge.control.http

import forge.control.http.dto.CreateEnvironmentRequest
import forge.control.http.dto.toResponse
import forge.control.service.EnvironmentService
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.environmentRoutes(environments: EnvironmentService) {
    route("/v1/projects/{projectId}/environments") {
        post {
            val projectId = call.parameters.requireUuid("projectId")
            val body = call.receive<CreateEnvironmentRequest>()
            val created = environments.create(projectId, body.name)
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            val projectId = call.parameters.requireUuid("projectId")
            call.respond(environments.list(projectId).map { it.toResponse() })
        }
    }
    get("/v1/environments/{environmentId}") {
        val id = call.parameters.requireUuid("environmentId")
        call.respond(environments.get(id).toResponse())
    }
}
