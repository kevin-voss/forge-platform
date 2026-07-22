package forge.control.scheduler.api

import forge.control.scheduler.NodeStore
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import io.ktor.server.routing.route

fun Route.nodeFleetRoutes(store: NodeStore) {
    route("/v1/nodes") {
        get {
            call.respond(store.list().map { it.toResponse() })
        }
    }
}
