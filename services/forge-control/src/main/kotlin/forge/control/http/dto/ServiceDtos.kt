package forge.control.http.dto

import forge.control.domain.Service
import kotlinx.serialization.Serializable

@Serializable
data class CreateServiceRequest(
    val name: String? = null,
    val port: Int? = null,
)

@Serializable
data class ServiceResponse(
    val id: String,
    val applicationId: String,
    val name: String,
    val port: Int,
    val createdAt: String,
    val updatedAt: String,
)

fun Service.toResponse(): ServiceResponse =
    ServiceResponse(
        id = id.toString(),
        applicationId = applicationId.toString(),
        name = name,
        port = port,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
    )
