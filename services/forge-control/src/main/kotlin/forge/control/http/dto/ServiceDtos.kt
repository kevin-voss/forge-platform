package forge.control.http.dto

import forge.control.domain.Service
import kotlinx.serialization.Serializable

@Serializable
data class CreateServiceRequest(
    val name: String? = null,
    val port: Int? = null,
)

@Serializable
data class RecordServiceImageRequest(
    val image: String? = null,
    val digest: String? = null,
    val commit: String? = null,
    val buildId: String? = null,
)

@Serializable
data class ServiceResponse(
    val id: String,
    val applicationId: String,
    val name: String,
    val port: Int,
    val createdAt: String,
    val updatedAt: String,
    val image: String? = null,
    val imageDigest: String? = null,
    val imageCommit: String? = null,
    val imageBuildId: String? = null,
)

fun Service.toResponse(): ServiceResponse =
    ServiceResponse(
        id = id.toString(),
        applicationId = applicationId.toString(),
        name = name,
        port = port,
        createdAt = createdAt.toString(),
        updatedAt = updatedAt.toString(),
        image = image,
        imageDigest = imageDigest,
        imageCommit = imageCommit,
        imageBuildId = imageBuildId,
    )
