package forge.control.scheduler.api

import forge.control.scheduler.FleetNode
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeAllocation
import forge.control.scheduler.NodeCapacity
import forge.control.scheduler.PeerInfo
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class RegisterNodeRequest(
    @SerialName("node_id") val nodeId: String? = null,
    val address: String? = null,
    val capacity: NodeCapacityDto? = null,
    @SerialName("bootstrap_token") val bootstrapToken: String? = null,
    @SerialName("wireguard_public_key") val wireguardPublicKey: String? = null,
)

@Serializable
data class NodeCapacityDto(
    val slots: Int? = null,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
)

@Serializable
data class HeartbeatRequest(
    val allocated: NodeResourcesDto? = null,
    val free: NodeResourcesDto? = null,
    @SerialName("running_replicas") val runningReplicas: List<String>? = null,
)

@Serializable
data class NodeResourcesDto(
    val slots: Int? = null,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
)

@Serializable
data class NetworkAssignmentDto(
    val cidr: String,
    val gateway: String,
)

@Serializable
data class PeerDto(
    @SerialName("node_id") val nodeId: String,
    @SerialName("public_key") val publicKey: String,
    val endpoint: String? = null,
    @SerialName("allowed_ips") val allowedIps: List<String> = emptyList(),
)

@Serializable
data class NodeResponse(
    val id: String,
    val address: String,
    val status: String,
    val capacity: NodeCapacityDto,
    val allocated: NodeResourcesDto,
    val free: NodeResourcesDto,
    @SerialName("running_replicas") val runningReplicas: List<String> = emptyList(),
    @SerialName("last_heartbeat_at") val lastHeartbeatAt: String,
    @SerialName("registered_at") val registeredAt: String,
    @SerialName("node_id") val nodeId: String? = null,
    val network: NetworkAssignmentDto? = null,
    val peers: List<PeerDto>? = null,
    @SerialName("wireguard_public_key") val wireguardPublicKey: String? = null,
)

fun NodeCapacityDto.toModel(): NodeCapacity? {
    val slots = slots ?: return null
    return NodeCapacity(slots = slots, cpuMillis = cpuMillis, memMb = memMb)
}

fun NodeCapacity.toDto(): NodeCapacityDto =
    NodeCapacityDto(slots = slots, cpuMillis = cpuMillis, memMb = memMb)

fun FleetNode.toResponse(peers: List<PeerInfo> = emptyList()): NodeResponse {
    val freeSlots = LivenessMonitor.freeSlots(this)
    val network = if (networkCidr != null && networkGateway != null) {
        NetworkAssignmentDto(cidr = networkCidr, gateway = networkGateway)
    } else {
        null
    }
    return NodeResponse(
        id = id,
        nodeId = id,
        address = address,
        status = status,
        capacity = capacity.toDto(),
        allocated = NodeResourcesDto(
            slots = allocation.slots,
            cpuMillis = allocation.cpuMillis,
            memMb = allocation.memMb,
        ),
        free = NodeResourcesDto(
            slots = freeSlots,
            cpuMillis = capacity.cpuMillis?.let { total ->
                allocation.cpuMillis?.let { used -> (total - used).coerceAtLeast(0) }
            },
            memMb = capacity.memMb?.let { total ->
                allocation.memMb?.let { used -> (total - used).coerceAtLeast(0) }
            },
        ),
        runningReplicas = allocation.runningReplicas,
        lastHeartbeatAt = lastHeartbeatAt.toString(),
        registeredAt = registeredAt.toString(),
        network = network,
        peers = when {
            network != null || peers.isNotEmpty() -> peers.map {
                PeerDto(
                    nodeId = it.nodeId,
                    publicKey = it.publicKey,
                    endpoint = it.endpoint,
                    allowedIps = it.allowedIps,
                )
            }
            else -> null
        },
        wireguardPublicKey = wireguardPublicKey,
    )
}

fun HeartbeatRequest.toAllocation(capacitySlots: Int): NodeAllocation {
    val allocatedSlots = allocated?.slots
        ?: free?.slots?.let { capacitySlots - it }
        ?: 0
    return NodeAllocation(
        slots = allocatedSlots.coerceAtLeast(0),
        cpuMillis = allocated?.cpuMillis,
        memMb = allocated?.memMb,
        runningReplicas = runningReplicas.orEmpty(),
    )
}
