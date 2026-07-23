package forge.control.resource.http

import forge.control.resource.ApplyRequest
import forge.control.resource.ApplyService
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.post
import io.ktor.server.routing.route

/** `POST /v1/apply` — multi-resource create/update with optional dry-run. */
fun Route.applyRoutes(applyService: ApplyService) {
    route("/v1/apply") {
        post {
            val body = call.receive<ApplyRequest>()
            call.respond(applyService.apply(body))
        }
    }
}
