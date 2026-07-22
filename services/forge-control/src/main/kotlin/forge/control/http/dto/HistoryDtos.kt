package forge.control.http.dto

import kotlinx.serialization.Serializable

@Serializable
data class DeploymentHistoryResponse(
    val deploymentId: String,
    val events: List<DeploymentEventView>,
)

@Serializable
data class DeploymentEventView(
    val at: String,
    val from: String,
    val to: String,
    val image: String? = null,
    val desiredReplicas: Int? = null,
    val actualReplicas: Int? = null,
    val reason: String? = null,
)
