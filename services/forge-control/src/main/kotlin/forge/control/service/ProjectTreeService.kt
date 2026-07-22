package forge.control.service

import forge.control.http.ApiException
import forge.control.http.dto.ApplicationTreeResponse
import forge.control.http.dto.ProjectTreeResponse
import forge.control.http.dto.ServiceTreeResponse
import forge.control.http.dto.toResponse
import forge.control.repo.ApplicationRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.ServiceRepository
import java.util.UUID

class ProjectTreeService(
    private val projects: ProjectRepository,
    private val environments: EnvironmentRepository,
    private val applications: ApplicationRepository,
    private val services: ServiceRepository,
    private val deployments: DeploymentRepository,
) {
    fun get(projectId: UUID): ProjectTreeResponse {
        val project = projects.findById(projectId)
            ?: throw ApiException.NotFound("project not found", mapOf("id" to projectId.toString()))
        return ProjectTreeResponse(
            project = project.toResponse(),
            environments = environments.list(projectId).map { it.toResponse() },
            applications = applications.list(projectId).map { application ->
                ApplicationTreeResponse(
                    id = application.id.toString(),
                    projectId = application.projectId.toString(),
                    name = application.name,
                    createdAt = application.createdAt.toString(),
                    updatedAt = application.updatedAt.toString(),
                    services = services.list(application.id).map { service ->
                        ServiceTreeResponse(
                            id = service.id.toString(),
                            applicationId = service.applicationId.toString(),
                            name = service.name,
                            port = service.port,
                            createdAt = service.createdAt.toString(),
                            updatedAt = service.updatedAt.toString(),
                            deployments = deployments.listByService(service.id).map { it.toResponse() },
                        )
                    },
                )
            },
        )
    }
}
