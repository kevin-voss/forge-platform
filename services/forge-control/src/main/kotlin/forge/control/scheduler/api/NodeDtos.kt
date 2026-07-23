package forge.control.scheduler.api

import forge.control.scheduler.FleetNode
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeAllocation
import forge.control.scheduler.NodeCapacity
import forge.control.scheduler.PeerInfo
import forge.control.scheduler.model.NodeTaint
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class RegisterNodeRequest(
    @SerialName("node_id") val nodeId: String? = null,
    val address: String? = null,
    val capacity: NodeCapacityDto? = null,
    @SerialName("bootstrap_token") val bootstrapToken: String? = null,
    @SerialName("wireguard_public_key") val wireguardPublicKey: String? = null,
    val labels: Map<String, String>? = null,
    val taints: List<NodeTaint>? = null,
    val architecture: String? = null,
    val os: String? = null,
    val provider: String? = null,
    val zone: String? = null,
    val region: String? = null,
    @SerialName("pool_id") val poolId: String? = null,
)

@Serializable
data class NodeCapacityDto(
    val slots: Int? = null,
    @SerialName("cpu_millis") val cpuMillis: Int? = null,
    @SerialName("mem_mb") val memMb: Int? = null,
    @SerialName("disk_mb") val diskMb: Int? = null,
    val gpu: forge.control.scheduler.model.GpuCapacity? = null,
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
    @SerialName("disk_mb") val diskMb: Int? = null,
    @SerialName("gpu_count") val gpuCount: Int? = null,
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
    val allocatable: NodeCapacityDto? = null,
    @SerialName("running_replicas") val runningReplicas: List<String> = emptyList(),
    @SerialName("last_heartbeat_at") val lastHeartbeatAt: String,
    @SerialName("registered_at") val registeredAt: String,
    @SerialName("node_id") val nodeId: String? = null,
    val network: NetworkAssignmentDto? = null,
    val peers: List<PeerDto>? = null,
    @SerialName("wireguard_public_key") val wireguardPublicKey: String? = null,
    val labels: Map<String, String> = emptyMap(),
    val taints: List<NodeTaint> = emptyList(),
    val architecture: String = "amd64",
    val os: String = "linux",
    val zone: String = "default",
    val region: String = "default",
    val provider: String = "docker",
)

fun NodeCapacityDto.toModel(): NodeCapacity? {
    val slots = slots ?: return null
    return NodeCapacity(
        slots = slots,
        cpuMillis = cpuMillis,
        memMb = memMb,
        diskMb = diskMb,
        gpu = gpu,
    )
}

fun NodeCapacity.toDto(): NodeCapacityDto =
    NodeCapacityDto(
        slots = slots,
        cpuMillis = cpuMillis,
        memMb = memMb,
        diskMb = diskMb,
        gpu = gpu,
    )

fun FleetNode.toResponse(peers: List<PeerInfo> = emptyList()): NodeResponse {
    val freeSlots = LivenessMonitor.freeSlots(this)
    val alloc = allocatable
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
        allocatable = alloc?.toDto(),
        allocated = NodeResourcesDto(
            slots = allocation.slots,
            cpuMillis = allocation.cpuMillis,
            memMb = allocation.memMb,
            diskMb = allocation.diskMb,
            gpuCount = allocation.gpuCount,
        ),
        free = NodeResourcesDto(
            slots = freeSlots,
            cpuMillis = (alloc?.cpuMillis ?: capacity.cpuMillis)?.let { total ->
                (total - (allocation.cpuMillis ?: 0)).coerceAtLeast(0)
            },
            memMb = (alloc?.memMb ?: capacity.memMb)?.let { total ->
                (total - (allocation.memMb ?: 0)).coerceAtLeast(0)
            },
            diskMb = (alloc?.diskMb ?: capacity.diskMb)?.let { total ->
                (total - (allocation.diskMb ?: 0)).coerceAtLeast(0)
            },
            gpuCount = (alloc?.gpu?.count ?: capacity.gpu?.count)?.let { total ->
                (total - (allocation.gpuCount ?: 0)).coerceAtLeast(0)
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
        labels = labels,
        taints = taints,
        architecture = architecture,
        os = os,
        zone = zone,
        region = region,
        provider = provider,
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
        diskMb = allocated?.diskMb,
        gpuCount = allocated?.gpuCount,
        runningReplicas = runningReplicas.orEmpty(),
    )
}
