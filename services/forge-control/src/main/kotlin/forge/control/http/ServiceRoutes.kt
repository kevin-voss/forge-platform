package forge.control.http

import forge.control.http.dto.CreateServiceRequest
import forge.control.http.dto.toResponse
import forge.control.service.ServiceService
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route

fun Route.serviceRoutes(services: ServiceService) {
    route("/v1/applications/{applicationId}/services") {
        post {
            val applicationId = call.parameters.requireUuid("applicationId")
            val body = call.receive<CreateServiceRequest>()
            call.respond(HttpStatusCode.Created, services.create(applicationId, body.name, body.port).toResponse())
        }
        get {
            val applicationId = call.parameters.requireUuid("applicationId")
            call.respond(services.list(applicationId).map { it.toResponse() })
        }
    }
    get("/v1/services/{serviceId}") {
        val serviceId = call.parameters.requireUuid("serviceId")
        call.respond(services.get(serviceId).toResponse())
    }
}
