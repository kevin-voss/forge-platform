package forge.control.http.dto

import forge.control.domain.Deployment
import kotlinx.serialization.Serializable

@Serializable
data class CreateDeploymentRequest(
    val image: String? = null,
    val desiredReplicas: Int? = null,
    val environmentId: String? = null,
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
