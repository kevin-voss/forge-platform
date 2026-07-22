package forge.control.http.dto

import forge.control.domain.Deployment
import kotlinx.serialization.Serializable

@Serializable
data class CreateDeploymentRequest(
    val image: String? = null,
    val desiredReplicas: Int? = null,
    val environmentId: String? = null,
)

/** Patch desired image / replica count to trigger reconcile (07.03+ rolling update). */
@Serializable
data class UpdateDeploymentRequest(
    val image: String? = null,
    val desiredReplicas: Int? = null,
)

/** Runtime → Control actual-state report (`04.07` / `04.08` contract). */
@Serializable
data class DeploymentStatusReportRequest(
    val status: String? = null,
    val nodeId: String? = null,
    val endpoint: EndpointReport? = null,
)

@Serializable
data class EndpointReport(
    val hostPort: Int? = null,
)

@Serializable
data class DeploymentResponse(
    val id: String,
    val serviceId: String,
    val environmentId: String,
    val image: String,
    val desiredReplicas: Int,
    val status: String,
    val createdAt: String,
    val updatedAt: String,
)

fun Deployment.toResponse(): DeploymentResponse =
    DeploymentResponse(
        id = id.toString(),
        serviceId = serviceId.toString(),
        environmentId = environmentId.toString(),
        image = image,
        desiredReplicas = desiredReplicas,
        status = status,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )
