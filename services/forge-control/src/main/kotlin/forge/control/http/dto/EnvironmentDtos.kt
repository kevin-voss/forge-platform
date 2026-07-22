package forge.control.http.dto

import forge.control.domain.Environment
import kotlinx.serialization.Serializable

@Serializable
data class CreateEnvironmentRequest(
    val name: String? = null,
)

@Serializable
data class EnvironmentResponse(
    val id: String,
    val projectId: String,
    val name: String,
    val createdAt: String,
    val updatedAt: String,
)

fun Environment.toResponse(): EnvironmentResponse =
    EnvironmentResponse(
        id = id.toString(),
        projectId = projectId.toString(),
        name = name,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )
