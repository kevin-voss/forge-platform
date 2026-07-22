package forge.control.http.dto

import kotlinx.serialization.Serializable

@Serializable
data class ProjectTreeResponse(
    val project: ProjectResponse,
    val environments: List<EnvironmentResponse>,
    val applications: List<ApplicationTreeResponse>,
)

@Serializable
data class ApplicationTreeResponse(
    val id: String,
    val projectId: String,
    val name: String,
    val createdAt: String,
    val updatedAt: String,
    val services: List<ServiceTreeResponse>,
)

@Serializable
data class ServiceTreeResponse(
    val id: String,
    val applicationId: String,
    val name: String,
    val port: Int,
    val createdAt: String,
    val updatedAt: String,
    val deployments: List<DeploymentResponse>,
    val image: String? = null,
    val imageDigest: String? = null,
    val imageCommit: String? = null,
    val imageBuildId: String? = null,
)
