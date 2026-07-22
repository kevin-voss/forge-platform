package forge.control.service

import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.ProjectRepository
import java.util.UUID

/** Verifies resource parents before child persistence; reusable by deployment APIs. */
class RelationshipValidator(
    private val projects: ProjectRepository,
    private val applications: ApplicationRepository,
) {
    fun requireProject(projectId: UUID) {
        if (projects.findById(projectId) == null) {
            throw ApiException.NotFound("project not found", mapOf("id" to projectId.toString()))
        }
    }

    fun requireApplication(applicationId: UUID) {
        if (applications.findById(applicationId) == null) {
            throw ApiException.NotFound("application not found", mapOf("id" to applicationId.toString()))
        }
    }
}
