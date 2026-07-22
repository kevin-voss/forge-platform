package forge.control.service

import forge.control.domain.Application
import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.AuditRepository
import forge.control.repo.RepositoryException
import java.util.UUID

class ApplicationService(
    private val applications: ApplicationRepository,
    private val relationships: RelationshipValidator,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) {
    fun create(projectId: UUID, nameRaw: String?): Application {
        relationships.requireProject(projectId)
        val name = ProjectService.validateName(nameRaw)
        val created = try {
            applications.create(projectId, name)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "application name already exists in project",
                mapOf("name" to name, "projectId" to projectId.toString()),
            )
        } catch (e: RepositoryException.ConstraintViolation) {
            relationships.requireProject(projectId)
            throw ApiException.BadRequest(e.message ?: "constraint violation")
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "application",
            entityId = created.id,
            action = "create",
            actor = actor,
            detailJson = """{"projectId":"$projectId","name":${jsonString(name)}}""",
        )
        return created
    }

    fun get(id: UUID): Application =
        applications.findById(id)
            ?: throw ApiException.NotFound("application not found", mapOf("id" to id.toString()))

    fun list(projectId: UUID): List<Application> {
        relationships.requireProject(projectId)
        return applications.list(projectId)
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
