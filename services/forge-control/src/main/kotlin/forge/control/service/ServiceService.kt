package forge.control.service

import forge.control.domain.Service
import forge.control.http.ApiException
import forge.control.repo.AuditRepository
import forge.control.repo.RepositoryException
import forge.control.repo.ServiceRepository
import java.util.UUID

class ServiceService(
    private val services: ServiceRepository,
    private val relationships: RelationshipValidator,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) {
    fun create(applicationId: UUID, nameRaw: String?, portRaw: Int?): Service {
        relationships.requireApplication(applicationId)
        val name = ProjectService.validateName(nameRaw)
        val port = validatePort(portRaw)
        val created = try {
            services.create(applicationId, name, port)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "service name already exists in application",
                mapOf("name" to name, "applicationId" to applicationId.toString()),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            relationships.requireApplication(applicationId)
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "service",
            entityId = created.id,
            action = "create",
            actor = actor,
            detailJson = """{"applicationId":"$applicationId","name":${jsonString(name)},"port":$port}""",
        )
        return created
    }

    fun get(id: UUID): Service =
        services.findById(id)
            ?: throw ApiException.NotFound("service not found", mapOf("id" to id.toString()))

    fun list(applicationId: UUID): List<Service> {
        relationships.requireApplication(applicationId)
        return services.list(applicationId)
    }

    fun recordImage(
        id: UUID,
        imageRaw: String?,
        digestRaw: String?,
        commitRaw: String?,
        buildIdRaw: String?,
    ): Service {
        get(id) // 404 if missing
        val image = validateImage(imageRaw)
        val digest = digestRaw?.trim()?.takeIf { it.isNotEmpty() }
        val commit = commitRaw?.trim()?.takeIf { it.isNotEmpty() }
        val buildId = buildIdRaw?.trim()?.takeIf { it.isNotEmpty() }
        val updated = try {
            services.recordImage(id, image, digest, commit, buildId)
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "service",
            entityId = updated.id,
            action = "record_image",
            actor = actor,
            detailJson = """{"image":${jsonString(image)},"digest":${jsonNullable(digest)},"commit":${jsonNullable(commit)},"buildId":${jsonNullable(buildId)}}""",
        )
        return updated
    }

    companion object {
        fun validatePort(portRaw: Int?): Int {
            val port = portRaw ?: throw ApiException.BadRequest("port is required", mapOf("field" to "port"))
            if (port !in 1..65535) {
                throw ApiException.BadRequest("port must be between 1 and 65535", mapOf("field" to "port"))
            }
            return port
        }

        fun validateImage(imageRaw: String?): String {
            val image = imageRaw?.trim()
            if (image.isNullOrEmpty()) {
                throw ApiException.BadRequest("image is required", mapOf("field" to "image"))
            }
            return image
        }
    }

    private fun mapRepo(e: RepositoryException): ApiException =
        when (e) {
            is RepositoryException.Conflict -> ApiException.Conflict(e.message ?: "conflict")
            is RepositoryException.NotFound -> ApiException.NotFound(e.message ?: "not found")
            is RepositoryException.ConstraintViolation -> ApiException.BadRequest(e.message ?: "constraint violation")
        }

    private fun jsonString(value: String): String =
        buildString {
            append('"')
            value.forEach { ch ->
                when (ch) {
                    '\\' -> append("\\\\")
                    '"' -> append("\\\"")
                    '\n' -> append("\\n")
                    '\r' -> append("\\r")
                    '\t' -> append("\\t")
                    else -> append(ch)
                }
            }
            append('"')
        }

    private fun jsonNullable(value: String?): String =
        if (value == null) "null" else jsonString(value)
}
