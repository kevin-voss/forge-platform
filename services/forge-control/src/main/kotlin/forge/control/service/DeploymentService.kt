package forge.control.service

import forge.control.domain.Deployment
import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.AuditRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.RepositoryException
import forge.control.repo.ServiceRepository
import java.util.UUID

class DeploymentService(
    private val deployments: DeploymentRepository,
    private val services: ServiceRepository,
    private val applications: ApplicationRepository,
    private val environments: EnvironmentRepository,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) {
    fun create(
        serviceId: UUID,
        imageRaw: String?,
        desiredReplicasRaw: Int?,
        environmentId: UUID,
    ): Deployment {
        val service = services.findById(serviceId)
            ?: throw ApiException.NotFound("service not found", mapOf("id" to serviceId.toString()))
        val environment = environments.findById(environmentId)
            ?: throw ApiException.NotFound("environment not found", mapOf("id" to environmentId.toString()))
        val application = applications.findById(service.applicationId)
            ?: throw ApiException.NotFound("application not found", mapOf("id" to service.applicationId.toString()))

        if (application.projectId != environment.projectId) {
            throw ApiException.BadRequest(
                "environment must belong to the service project",
                mapOf(
                    "serviceId" to serviceId.toString(),
                    "environmentId" to environmentId.toString(),
                ),
            )
        }

        val image = validateImage(imageRaw)
        val desiredReplicas = validateDesiredReplicas(desiredReplicasRaw)
        val created = try {
            deployments.create(serviceId, environmentId, image, desiredReplicas, PENDING)
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "deployment",
            entityId = created.id,
            action = "create",
            actor = actor,
            detailJson = """{"serviceId":"$serviceId","environmentId":"$environmentId","image":${jsonString(image)},"desiredReplicas":$desiredReplicas,"status":"$PENDING"}""",
        )
        return created
    }

    fun get(id: UUID): Deployment =
        deployments.findById(id)
            ?: throw ApiException.NotFound("deployment not found", mapOf("id" to id.toString()))

    fun list(serviceId: UUID): List<Deployment> {
        if (services.findById(serviceId) == null) {
            throw ApiException.NotFound("service not found", mapOf("id" to serviceId.toString()))
        }
        return deployments.listByService(serviceId)
    }

    /** Internal-only status hook for runtime/reconciler integration and tests. */
    internal fun updateStatus(id: UUID, status: String): Deployment {
        require(status in STATUSES) { "invalid deployment status" }
        val existing = get(id)
        val updated = deployments.update(id, status = status)
        if (existing.status != updated.status) {
            audit.append(
                entityType = "deployment",
                entityId = id,
                action = "status_change",
                actor = actor,
                detailJson = """{"old":${jsonString(existing.status)},"new":${jsonString(updated.status)}}""",
            )
        }
        return updated
    }

    companion object {
        const val PENDING = "pending"
        private val STATUSES = setOf("pending", "active", "failed", "stopped")

        fun validateImage(imageRaw: String?): String {
            val image = imageRaw?.trim()
            if (image.isNullOrEmpty()) {
                throw ApiException.BadRequest("image is required", mapOf("field" to "image"))
            }
            return image
        }

        fun validateDesiredReplicas(desiredReplicasRaw: Int?): Int {
            val desiredReplicas = desiredReplicasRaw ?: 1
            if (desiredReplicas < 0) {
                throw ApiException.BadRequest(
                    "desiredReplicas must be greater than or equal to 0",
                    mapOf("field" to "desiredReplicas"),
                )
            }
            return desiredReplicas
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
}
