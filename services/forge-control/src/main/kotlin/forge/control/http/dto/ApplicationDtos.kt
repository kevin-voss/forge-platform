package forge.control.http.dto

import forge.control.domain.Application
import kotlinx.serialization.Serializable

@Serializable
data class CreateApplicationRequest(
    val name: String? = null,
)

@Serializable
data class ApplicationResponse(
    val id: String,
    val projectId: String,
    val name: String,
    val createdAt: String,
    val updatedAt: String,
)

fun Application.toResponse(): ApplicationResponse =
    ApplicationResponse(
        id = id.toString(),
        projectId = projectId.toString(),
        name = name,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )
