package forge.control.http.dto

import forge.control.domain.Project
import kotlinx.serialization.Serializable

@Serializable
data class CreateProjectRequest(
    val name: String? = null,
    val slug: String? = null,
)

@Serializable
data class ProjectResponse(
    val id: String,
    val name: String,
    val slug: String,
    val createdAt: String,
    val updatedAt: String,
)

fun Project.toResponse(): ProjectResponse =
    ProjectResponse(
        id = id.toString(),
        name = name,
        slug = slug,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )
