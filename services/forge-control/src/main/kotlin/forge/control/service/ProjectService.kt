package forge.control.service

import forge.control.domain.Project
import forge.control.http.ApiException
import forge.control.repo.AuditRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import java.util.UUID

class ProjectService(
    private val projects: ProjectRepository,
    private val audit: AuditRepository,
    private val actor: String = "dev",
) {
    fun create(nameRaw: String?, slugRaw: String?): Project {
        val name = validateName(nameRaw)
        val slug = resolveSlug(name, slugRaw)
        val created = try {
            projects.create(name, slug)
        } catch (e: RepositoryException.Conflict) {
            throw ApiException.Conflict(
                "project slug already exists",
                mapOf("slug" to slug),
            )
        } catch (e: RepositoryException) {
            throw mapRepo(e)
        }
        audit.append(
            entityType = "project",
            entityId = created.id,
            action = "create",
            actor = actor,
            detailJson = """{"name":${jsonString(name)},"slug":${jsonString(slug)}}""",
        )
        return created
    }

    fun get(id: UUID): Project =
        projects.findById(id)
            ?: throw ApiException.NotFound("project not found", mapOf("id" to id.toString()))

    fun list(): List<Project> = projects.list()

    private fun resolveSlug(name: String, slugRaw: String?): String {
        val slug = when {
            slugRaw == null -> Slug.derive(name)
            else -> Slug.normalize(slugRaw)
                ?: throw ApiException.BadRequest(
                    "slug must not be blank",
                    mapOf("field" to "slug"),
                )
        }
        if (slug.isBlank()) {
            throw ApiException.BadRequest(
                "could not derive slug from name; provide an explicit slug",
                mapOf("field" to "slug"),
            )
        }
        Slug.validationError(slug)?.let { msg ->
            throw ApiException.BadRequest(msg, mapOf("field" to "slug"))
        }
        return slug
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
