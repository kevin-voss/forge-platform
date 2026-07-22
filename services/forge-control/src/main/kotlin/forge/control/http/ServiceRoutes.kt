package forge.control.http

import forge.control.http.dto.CreateServiceRequest
import forge.control.http.dto.RecordServiceImageRequest
import forge.control.http.dto.toResponse
import forge.control.service.ServiceService
import forge.control.repo.IdempotencyStore
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import kotlinx.serialization.json.Json

fun Route.serviceRoutes(services: ServiceService, idempotency: IdempotencyStore? = null) {
    route("/v1/applications/{applicationId}/services") {
        post {
            val applicationId = call.parameters.requireUuid("applicationId")
            val body = call.receive<CreateServiceRequest>()
            call.idempotentCreate(idempotency, "service", Json.encodeToString(CreateServiceRequest.serializer(), body)) {
                val created = services.create(applicationId, body.name, body.port)
                created.id to Json.encodeToJsonElement(forge.control.http.dto.ServiceResponse.serializer(), created.toResponse())
            }
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
    post("/v1/services/{serviceId}/image") {
        val serviceId = call.parameters.requireUuid("serviceId")
        val body = call.receive<RecordServiceImageRequest>()
        call.idempotentAction(
            idempotency,
            "service-image",
            Json.encodeToString(RecordServiceImageRequest.serializer(), body),
            HttpStatusCode.OK,
        ) {
            val updated = services.recordImage(serviceId, body.image, body.digest, body.commit, body.buildId)
            updated.id to Json.encodeToJsonElement(forge.control.http.dto.ServiceResponse.serializer(), updated.toResponse())
        }
    }
}
