package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeCapacity
import forge.control.scheduler.NodeStore
import forge.control.telemetry.Telemetry
import io.ktor.http.HttpStatusCode
import io.ktor.server.request.receive
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.post
import io.ktor.server.routing.route
import java.time.Instant

fun Route.nodeRegistrationRoutes(
    store: NodeStore,
    log: JsonLog,
    strictRegister: Boolean = false,
    telemetry: Telemetry = Telemetry.current(),
    clock: () -> Instant = { Instant.now() },
    onRegistered: (() -> Unit)? = null,
) {
    route("/v1/nodes") {
        post("/register") {
            val body = call.receive<RegisterNodeRequest>()
            val nodeId = body.nodeId?.trim().orEmpty()
            if (nodeId.isEmpty()) {
                throw ApiException.BadRequest(
                    "node_id is required",
                    mapOf("field" to "node_id"),
                )
            }
            val address = body.address?.trim().orEmpty()
            if (address.isEmpty()) {
                throw ApiException.BadRequest(
                    "address is required",
                    mapOf("field" to "address"),
                )
            }
            val capacity = body.capacity?.toModel()
                ?: throw ApiException.BadRequest(
                    "capacity.slots is required",
                    mapOf("field" to "capacity.slots"),
                )
            if (capacity.slots < 1) {
                throw ApiException.BadRequest(
                    "capacity.slots must be >= 1",
                    mapOf("field" to "capacity.slots"),
                )
            }

            val existing = store.find(nodeId)
            val created = existing == null
            val node = store.register(
                id = nodeId,
                address = address,
                capacity = capacity,
                at = clock(),
            )
            log.info(
                if (created) "node registered" else "node registration idempotent",
                "node_id" to node.id,
                "address" to node.address,
                "slots" to node.capacity.slots,
                "status" to node.status,
            )
            telemetry.recordNodeStatus(node.status)
            if (created) {
                try {
                    onRegistered?.invoke()
                } catch (_: Exception) {
                    // Queue drain failures must not fail registration.
                }
            }
            call.respond(
                if (created) HttpStatusCode.Created else HttpStatusCode.OK,
                node.toResponse(),
            )
        }

        post("/{id}/heartbeat") {
            val id = call.parameters["id"]?.trim().orEmpty()
            if (id.isEmpty()) {
                throw ApiException.BadRequest(
                    "node id is required",
                    mapOf("field" to "id"),
                )
            }
            val body = call.receive<HeartbeatRequest>()
            val at = clock()
            val span = telemetry.startSpan("node.heartbeat")
            span.setAttribute("node.id", id)
            try {
                var node = store.find(id)
                if (node == null) {
                    if (strictRegister) {
                        throw ApiException.NotFound(
                            "node not registered",
                            mapOf("node_id" to id),
                        )
                    }
                    log.warn(
                        "heartbeat from unknown node; auto-registering",
                        "node_id" to id,
                    )
                    val slots = body.allocated?.slots?.let { allocated ->
                        body.free?.slots?.let { free -> allocated + free } ?: (allocated + 1)
                    } ?: body.free?.slots ?: 1
                    node = store.register(
                        id = id,
                        address = "unknown",
                        capacity = NodeCapacity(slots = slots.coerceAtLeast(1)),
                        at = at,
                    )
                }

                // Slot accounting is driven by CapacityReservation; heartbeats must not
                // shrink reserved slots before containers appear (or on shared-socket noise).
                // They may raise slots toward the observed running count and always refresh
                // running_replicas + liveness timestamp.
                val incoming = body.toAllocation(node.capacity.slots)
                val allocation = incoming.copy(
                    slots = maxOf(node.allocation.slots, incoming.slots),
                )
                val updated = store.heartbeat(id, allocation, at)
                    ?: throw ApiException.NotFound(
                        "node not registered",
                        mapOf("node_id" to id),
                    )
                telemetry.recordNodeFreeSlots(
                    updated.id,
                    LivenessMonitor.freeSlots(updated),
                )
                call.respond(HttpStatusCode.OK, updated.toResponse())
            } finally {
                span.end()
            }
        }
    }
}
