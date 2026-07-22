package forge.control.scheduler.api

import forge.control.scheduler.Placement
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class CreatePlacementRequest(
    @SerialName("deployment_id") val deploymentId: String? = null,
    @SerialName("replica_index") val replicaIndex: Int? = null,
    val requirements: PlacementRequirementsDto? = null,
    @SerialName("anti_affinity") val antiAffinity: String? = null,
    @SerialName("service_id") val serviceId: String? = null,
)

@Serializable
data class PlacementRequirementsDto(
    val slots: Int? = null,
)

@Serializable
data class PlacementResponse(
    @SerialName("placement_id") val placementId: String,
    @SerialName("deployment_id") val deploymentId: String,
    @SerialName("replica_index") val replicaIndex: Int,
    @SerialName("node_id") val nodeId: String? = null,
    val strategy: String? = null,
    val reason: String? = null,
    val status: String = "placed",
    @SerialName("anti_affinity") val antiAffinity: String = "soft",
)

fun Placement.toResponse(): PlacementResponse =
    PlacementResponse(
        placementId = id,
        deploymentId = deploymentId.toString(),
        replicaIndex = replicaIndex,
        nodeId = nodeId,
        strategy = strategy,
        reason = reason,
        status = status,
        antiAffinity = antiAffinity,
    )
