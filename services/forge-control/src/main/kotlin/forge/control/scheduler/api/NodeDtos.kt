package forge.control.scheduler.api

import forge.control.scheduler.FleetNode
import forge.control.scheduler.LivenessMonitor
import forge.control.scheduler.NodeAllocation
import forge.control.scheduler.NodeCapacity
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class RegisterNodeRequest(
    @SerialName("node_id") val nodeId: String? = null,
    val address: String? = null,
    val capacity: NodeCapacityDto? = null,
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
)

fun NodeCapacityDto.toModel(): NodeCapacity? {
    val slots = slots ?: return null
    return NodeCapacity(slots = slots, cpuMillis = cpuMillis, memMb = memMb)
}

fun NodeCapacity.toDto(): NodeCapacityDto =
    NodeCapacityDto(slots = slots, cpuMillis = cpuMillis, memMb = memMb)

fun FleetNode.toResponse(): NodeResponse {
    val freeSlots = LivenessMonitor.freeSlots(this)
    return NodeResponse(
        id = id,
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
