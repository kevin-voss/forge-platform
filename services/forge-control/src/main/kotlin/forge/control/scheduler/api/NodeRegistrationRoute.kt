package forge.control.scheduler.api

import forge.control.http.ApiException
import forge.control.logging.JsonLog
import forge.control.scheduler.JoinRegisterCommand
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeCapacity
import forge.control.scheduler.NodeJoinOrchestrator
import forge.control.scheduler.NodeLabelMerger
import forge.control.scheduler.NodeSchedulingFacts
import forge.control.scheduler.NodeStore
import forge.control.scheduler.TaintChangeHandler
import forge.control.scheduler.model.NodeTaint
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
    joinOrchestrator: NodeJoinOrchestrator? = null,
    taintChangeHandler: TaintChangeHandler? = null,
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

            val facts = body.toSchedulingFacts()
            val previousTaints = store.find(nodeId)?.taints.orEmpty()

            val orchestrator = joinOrchestrator
            if (orchestrator != null) {
                val result = orchestrator.register(
                    JoinRegisterCommand(
                        nodeId = nodeId,
                        address = address,
                        capacity = capacity,
                        bootstrapToken = body.bootstrapToken,
                        wireguardPublicKey = body.wireguardPublicKey,
                        facts = facts,
                    ),
                )
                logRegistration(log, result.created, result.node, facts)
                telemetry.recordNodeStatus(result.node.status)
                telemetry.recordNodeArchOs(result.node.architecture, result.node.os)
                taintChangeHandler?.onTaintsChanged(nodeId, previousTaints, result.node.taints)
                if (result.created) {
                    try {
                        onRegistered?.invoke()
                    } catch (_: Exception) {
                        // Queue drain failures must not fail registration.
                    }
                }
                val peers = if (result.node.networkCidr != null) result.peers else emptyList()
                call.respond(
                    if (result.created) HttpStatusCode.Created else HttpStatusCode.OK,
                    result.node.toResponse(peers),
                )
                return@post
            }

            val existing = store.find(nodeId)
            val created = existing == null
            val node = store.register(
                id = nodeId,
                address = address,
                capacity = capacity,
                at = clock(),
                facts = facts,
            )
            logRegistration(log, created, node, facts)
            telemetry.recordNodeStatus(node.status)
            telemetry.recordNodeArchOs(node.architecture, node.os)
            taintChangeHandler?.onTaintsChanged(nodeId, previousTaints, node.taints)
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

                if (node.keyRevoked) {
                    throw ApiException.Unauthorized(
                        "node key revoked; re-register with a fresh bootstrap token",
                        details = mapOf("node_id" to id),
                        code = "InvalidBootstrapToken",
                    )
                }

                val fromStatus = node.status
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
                if (fromStatus == "joining" && updated.status == "online") {
                    log.info(
                        "node join status transition",
                        "node_id" to id,
                        "from_status" to fromStatus,
                        "to_status" to "online",
                    )
                    telemetry.recordNodeStatus("online")
                }
                telemetry.recordNodeFreeSlots(
                    updated.id,
                    LivenessMonitor.freeSlots(updated),
                )
                call.respond(HttpStatusCode.OK, updated.toResponse())
            } finally {
                span.end()
            }
        }

        post("/{id}/revoke-key") {
            val id = call.parameters["id"]?.trim().orEmpty()
            if (id.isEmpty()) {
                throw ApiException.BadRequest(
                    "node id is required",
                    mapOf("field" to "id"),
                )
            }
            val orchestrator = joinOrchestrator
                ?: throw ApiException.ServiceUnavailable("join orchestrator not configured")
            val revoked = orchestrator.revokeKey(id, clock())
                ?: throw ApiException.NotFound(
                    "node not registered",
                    mapOf("node_id" to id),
                )
            telemetry.recordNodeStatus(revoked.status)
            call.respond(HttpStatusCode.OK, revoked.toResponse())
        }
    }
}

private fun RegisterNodeRequest.toSchedulingFacts(): NodeSchedulingFacts {
    val arch = architecture?.trim()?.takeIf { it.isNotEmpty() } ?: "amd64"
    val nodeOs = os?.trim()?.takeIf { it.isNotEmpty() } ?: "linux"
    val providerLabel = provider?.trim()?.takeIf { it.isNotEmpty() }
        ?: labels?.get(NodeLabelMerger.LABEL_PROVIDER)
        ?: "unknown"
    val agentLabels = labels.orEmpty().filterKeys { it.isNotBlank() }
    val parsedTaints = taints.orEmpty().mapNotNull { taint ->
        val key = taint.key.trim()
        if (key.isEmpty()) return@mapNotNull null
        val effect = taint.effect.trim()
        if (effect != "NoSchedule" && effect != "NoExecute") return@mapNotNull null
        NodeTaint(key = key, value = taint.value, effect = effect)
    }
    return NodeSchedulingFacts(
        agentLabels = agentLabels,
        taints = parsedTaints,
        architecture = arch,
        os = nodeOs.lowercase(),
        provider = providerLabel,
    )
}

private fun logRegistration(
    log: JsonLog,
    created: Boolean,
    node: forge.control.scheduler.FleetNode,
    facts: NodeSchedulingFacts,
) {
    val conflicts = NodeLabelMerger.merge(
        nodeId = node.id,
        architecture = node.architecture,
        os = node.os,
        provider = facts.provider,
        poolLabels = facts.poolLabels,
        agentLabels = facts.agentLabels,
    ).conflicts
    for (conflict in conflicts) {
        log.info(
            "node label conflict",
            "node_id" to node.id,
            "key" to conflict.key,
            "pool_value" to conflict.poolValue,
            "node_value" to conflict.nodeValue,
        )
    }
    log.info(
        if (created) "node registered" else "node registration idempotent",
        "node_id" to node.id,
        "address" to node.address,
        "slots" to node.capacity.slots,
        "status" to node.status,
        "labels" to node.labels.toString(),
        "taints" to node.taints.toString(),
        "architecture" to node.architecture,
        "os" to node.os,
    )
}
