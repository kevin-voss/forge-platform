package forge.control.scheduler.api

import forge.control.scheduler.Placement
import forge.control.scheduler.model.PlacementTrace
import forge.control.scheduler.model.PlatformSpec
import forge.control.scheduler.model.ResourceBundle
import forge.control.scheduler.model.Toleration
import forge.control.scheduler.model.UnschedulableReasonEntry
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class CreatePlacementRequest(
    @SerialName("deployment_id") val deploymentId: String? = null,
    @SerialName("replica_index") val replicaIndex: Int? = null,
    val requirements: PlacementRequirementsDto? = null,
    @SerialName("anti_affinity") val antiAffinity: String? = null,
    @SerialName("service_id") val serviceId: String? = null,
    val placement: PlacementConstraintsDto? = null,
    val platform: PlatformSpec? = null,
)

@Serializable
data class PlacementConstraintsDto(
    val nodeSelector: Map<String, String>? = null,
    @SerialName("node_selector") val nodeSelectorSnake: Map<String, String>? = null,
    val tolerations: List<Toleration>? = null,
) {
    fun resolvedNodeSelector(): Map<String, String> =
        nodeSelector ?: nodeSelectorSnake ?: emptyMap()

    fun resolvedTolerations(): List<Toleration> = tolerations.orEmpty()
}

@Serializable
data class PlacementRequirementsDto(
    val slots: Int? = null,
    val requests: ResourceBundle? = null,
    val limits: ResourceBundle? = null,
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
    @SerialName("rescheduled_from_node") val rescheduledFromNode: String? = null,
    val slots: Int = 1,
    @SerialName("service_id") val serviceId: String? = null,
    val requests: ResourceBundle? = null,
    val limits: ResourceBundle? = null,
    val trace: PlacementTrace? = null,
    @SerialName("unschedulable_reasons") val unschedulableReasons: List<UnschedulableReasonEntry>? = null,
    val placement: PlacementConstraintsDto? = null,
    val platform: PlatformSpec? = null,
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
        rescheduledFromNode = rescheduledFromNode,
        slots = slots.coerceAtLeast(1),
        serviceId = serviceId,
        requests = requests,
        limits = limits,
        trace = trace,
        unschedulableReasons = unschedulableReasons.takeIf { it.isNotEmpty() },
        placement = if (!nodeSelector.isNullOrEmpty() || tolerations.isNotEmpty()) {
            PlacementConstraintsDto(
                nodeSelector = nodeSelector,
                tolerations = tolerations.takeIf { it.isNotEmpty() },
            )
        } else {
            null
        },
        platform = platform,
    )
