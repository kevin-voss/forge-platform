package forge.control.http

import forge.control.http.dto.CreateProjectRequest
import forge.control.http.dto.toResponse
import forge.control.service.ProjectService
import forge.control.service.ProjectTreeService
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.projectRoutes(projects: ProjectService, projectTrees: ProjectTreeService) {
    route("/v1/projects") {
        post {
            val body = call.receive<CreateProjectRequest>()
            val created = projects.create(body.name, body.slug)
            call.respond(HttpStatusCode.Created, created.toResponse())
        }
        get {
            call.respond(projects.list().map { it.toResponse() })
        }
        get("{projectId}") {
            val id = call.parameters.requireUuid("projectId")
            if (call.request.queryParameters["expand"] == "tree") {
                call.respond(projectTrees.get(id))
            } else {
                call.respond(projects.get(id).toResponse())
            }
        }
    }
}
