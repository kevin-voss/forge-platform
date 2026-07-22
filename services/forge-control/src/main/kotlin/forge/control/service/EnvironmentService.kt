package forge.control.service

import forge.control.domain.Environment
import forge.control.http.ApiException
import forge.control.repo.AuditRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import java.util.UUID

class EnvironmentService(
    private val projects: ProjectRepository,
    private val environments: EnvironmentRepository,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) {
    fun create(projectId: UUID, nameRaw: String?): Environment {
        ensureProject(projectId)
        val name = validateName(nameRaw)
        val created = try {
            environments.create(projectId, name)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "environment name already exists in project",
                mapOf("name" to name, "projectId" to projectId.toString()),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            // FK miss races with delete, or other DB check failures.
            if (projects.findById(projectId) == null) {
                throw ApiException.NotFound(
                    "project not found",
                    mapOf("id" to projectId.toString()),
                )
            }
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "environment",
            entityId = created.id,
            action = "create",
            actor = actor,
            detailJson = """{"projectId":"$projectId","name":${jsonString(name)}}""",
        )
        return created
    }

    fun get(id: UUID): Environment =
        environments.findById(id)
            ?: throw ApiException.NotFound("environment not found", mapOf("id" to id.toString()))

    fun list(projectId: UUID): List<Environment> {
        ensureProject(projectId)
        return environments.list(projectId)
    }

    private fun ensureProject(projectId: UUID) {
        if (projects.findById(projectId) == null) {
            throw ApiException.NotFound("project not found", mapOf("id" to projectId.toString()))
        }
    }

    companion object {
        fun validateName(nameRaw: String?): String {
            if (nameRaw == null) {
                throw ApiException.BadRequest("name is required", mapOf("field" to "name"))
            }
            val name = nameRaw.trim()
            if (name.isEmpty()) {
                throw ApiException.BadRequest("name must not be blank", mapOf("field" to "name"))
            }
            if (name.length > Slug.MAX_NAME_LENGTH) {
                throw ApiException.BadRequest(
                    "name must be at most ${Slug.MAX_NAME_LENGTH} characters",
                    mapOf("field" to "name"),
                )
            }
            return name
        }

        private fun jsonString(value: String): String =
            buildString {
                append('"')
                for (ch in value) {
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

        private fun mapRepo(e: RepositoryException): ApiException =
            when (e) {
                is RepositoryException.Conflict ->
                    ApiException.Conflict(e.message ?: "conflict")
                is RepositoryException.NotFound ->
                    ApiException.NotFound(e.message ?: "not found")
                is RepositoryException.ConstraintViolation ->
                    ApiException.BadRequest(e.message ?: "constraint violation")
            }
    }
}
