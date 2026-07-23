package forge.control

import forge.control.domain.Application
import forge.control.domain.AuditEntry
import forge.control.domain.Deployment
import forge.control.domain.Environment
import forge.control.domain.Project
import forge.control.domain.Service
import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.AuditRepository
import forge.control.repo.DeploymentRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.ServiceRepository
import forge.control.service.DeploymentService
import forge.control.service.ProjectTreeService
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class DeploymentServiceTest {
    private val projectId = UUID.randomUUID()
    private val otherProjectId = UUID.randomUUID()
    private val applicationId = UUID.randomUUID()
    private val serviceId = UUID.randomUUID()
    private val environmentId = UUID.randomUUID()
    private val now = Instant.parse("2026-01-01T00:00:00Z")
    private val service = Service(serviceId, applicationId, "api", 8080, now, now)
    private val application = Application(applicationId, projectId, "web", now, now)
    private val environment = Environment(environmentId, projectId, "development", now, now)

    @Test
    fun validatesDesiredStateAndAuditsCreation() {
        val deployments = FakeDeployments()
        val audit = FakeAudit()
        val deploymentService = service(deployments, audit)

        val created = deploymentService.create(serviceId, " registry.local/api:1 ", null, environmentId)

        assertEquals("registry.local/api:1", created.image)
        assertEquals(1, created.desiredReplicas)
        assertEquals(DeploymentService.PENDING, created.status)
        assertEquals(listOf("create"), audit.entries.map { it.action })
        assertFailsWith<ApiException.BadRequest> {
            deploymentService.create(serviceId, " ", 1, environmentId)
        }
        assertFailsWith<ApiException.BadRequest> {
            deploymentService.create(serviceId, "registry.local/api:1", -1, environmentId)
        }
    }

    @Test
    fun rejectsEnvironmentFromAnotherProject() {
        val deploymentService = service(
            FakeDeployments(),
            FakeAudit(),
            EnvironmentRepositoryFake(Environment(environmentId, otherProjectId, "production", now, now)),
        )

        assertFailsWith<ApiException.BadRequest> {
            deploymentService.create(serviceId, "registry.local/api:1", 1, environmentId)
        }
    }

    @Test
    fun reportStatusUpdatesAndAudits() {
        val deployments = FakeDeployments()
        val audit = FakeAudit()
        val deploymentService = service(deployments, audit)
        val created = deploymentService.create(serviceId, "registry.local/api:1", 1, environmentId)

        val active = deploymentService.reportStatus(created.id, "active", "node-1", 49152)
        assertEquals("active", active.status)
        assertEquals(listOf("create", "status_change"), audit.entries.map { it.action })

        assertFailsWith<ApiException.BadRequest> {
            deploymentService.reportStatus(created.id, "bogus", "node-1", null)
        }
        assertFailsWith<ApiException.BadRequest> {
            deploymentService.reportStatus(created.id, "failed", " ", null)
        }
    }

    @Test
    fun deleteRemovesDeploymentAndAudits() {
        val deployments = FakeDeployments()
        val audit = FakeAudit()
        val deploymentService = service(deployments, audit)
        val created = deploymentService.create(serviceId, "registry.local/api:1", 1, environmentId)

        deploymentService.delete(created.id)
        assertFailsWith<ApiException.NotFound> { deploymentService.get(created.id) }
        assertEquals(listOf("create", "delete"), audit.entries.map { it.action })
    }

    @Test
    fun updateDesiredPatchesImageAndReplicas() {
        val deployments = FakeDeployments()
        val audit = FakeAudit()
        val deploymentService = service(deployments, audit)
        val created = deploymentService.create(serviceId, "registry.local/api:1", 1, environmentId)

        val updated = deploymentService.updateDesired(created.id, " registry.local/api:2 ", 2)
        assertEquals("registry.local/api:2", updated.image)
        assertEquals(2, updated.desiredReplicas)
        assertEquals(listOf("create", "update"), audit.entries.map { it.action })

        assertFailsWith<ApiException.BadRequest> {
            deploymentService.updateDesired(created.id, null, null)
        }
        assertFailsWith<ApiException.BadRequest> {
            deploymentService.updateDesired(created.id, " ", null)
        }
    }

    @Test
    fun assemblesCompleteProjectTree() {
        val deployment = Deployment(
            UUID.randomUUID(),
            serviceId,
            environmentId,
            "registry.local/api:1",
            1,
            "pending",
            now,
            now,
        )
        val tree = ProjectTreeService(
            ProjectRepositoryFake(Project(projectId, "Acme", "acme", now, now)),
            EnvironmentRepositoryFake(environment),
            ApplicationRepositoryFake(application),
            ServiceRepositoryFake(service),
            FakeDeployments(listOf(deployment)),
        ).get(projectId)

        assertEquals(environmentId.toString(), tree.environments.single().id)
        assertEquals(applicationId.toString(), tree.applications.single().id)
        assertEquals(serviceId.toString(), tree.applications.single().services.single().id)
        assertEquals(deployment.id.toString(), tree.applications.single().services.single().deployments.single().id)
    }

    private fun service(
        deployments: DeploymentRepository,
        audit: AuditRepository,
        environments: EnvironmentRepository = EnvironmentRepositoryFake(environment),
    ) = DeploymentService(
        deployments,
        ServiceRepositoryFake(service),
        ApplicationRepositoryFake(application),
        environments,
        audit,
    )

    private class ProjectRepositoryFake(private val project: Project) : ProjectRepository {
        override fun create(name: String, slug: String): Project = error("not used")
        override fun findById(id: UUID): Project? = project.takeIf { it.id == id }
        override fun findBySlug(slug: String): Project? = project.takeIf { it.slug == slug }
        override fun list(): List<Project> = listOf(project)
        override fun update(id: UUID, name: String?, slug: String?): Project = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class EnvironmentRepositoryFake(private val environment: Environment) : EnvironmentRepository {
        override fun create(projectId: UUID, name: String): Environment = error("not used")
        override fun findById(id: UUID): Environment? = environment.takeIf { it.id == id }
        override fun findByProjectAndName(projectId: UUID, name: String): Environment? =
            environment.takeIf { it.projectId == projectId && it.name == name }
        override fun list(projectId: UUID): List<Environment> = listOf(environment).filter { it.projectId == projectId }
        override fun update(id: UUID, name: String): Environment = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class ApplicationRepositoryFake(private val application: Application) : ApplicationRepository {
        override fun create(projectId: UUID, name: String): Application = error("not used")
        override fun findById(id: UUID): Application? = application.takeIf { it.id == id }
        override fun findByProjectAndName(projectId: UUID, name: String): Application? =
            application.takeIf { it.projectId == projectId && it.name == name }
        override fun list(projectId: UUID): List<Application> = listOf(application).filter { it.projectId == projectId }
        override fun update(id: UUID, name: String): Application = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class ServiceRepositoryFake(private val service: Service) : ServiceRepository {
        override fun create(applicationId: UUID, name: String, port: Int): Service = error("not used")
        override fun findById(id: UUID): Service? = service.takeIf { it.id == id }
        override fun findByApplicationAndName(applicationId: UUID, name: String): Service? =
            service.takeIf { it.applicationId == applicationId && it.name == name }
        override fun list(applicationId: UUID): List<Service> = listOf(service).filter { it.applicationId == applicationId }
        override fun update(id: UUID, name: String?, port: Int?): Service = error("not used")
        override fun recordImage(
            id: UUID,
            image: String,
            digest: String?,
            commit: String?,
            buildId: String?,
        ): Service = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class FakeDeployments(
        initial: List<Deployment> = emptyList(),
    ) : DeploymentRepository {
        private val rows = initial.toMutableList()

        override fun create(
            serviceId: UUID,
            environmentId: UUID,
            image: String,
            desiredReplicas: Int,
            status: String,
            rolloutBatchSize: Int,
            rolloutTimeoutSeconds: Int,
            name: String,
        ): Deployment = Deployment(
            UUID.randomUUID(), serviceId, environmentId, image, desiredReplicas, status, NOW, NOW,
            rolloutBatchSize, rolloutTimeoutSeconds, name,
        ).also(rows::add)

        override fun findById(id: UUID): Deployment? = rows.find { it.id == id }
        override fun findByEnvironmentAndName(environmentId: UUID, name: String): Deployment? =
            rows.find { it.environmentId == environmentId && it.name == name }
        override fun listByService(serviceId: UUID): List<Deployment> = rows.filter { it.serviceId == serviceId }
        override fun listAll(): List<Deployment> = rows.toList()
        override fun update(id: UUID, image: String?, desiredReplicas: Int?, status: String?): Deployment {
            val idx = rows.indexOfFirst { it.id == id }
            if (idx < 0) throw forge.control.repo.RepositoryException.NotFound("deployment", id)
            val existing = rows[idx]
            val updated = existing.copy(
                image = image ?: existing.image,
                desiredReplicas = desiredReplicas ?: existing.desiredReplicas,
                status = status ?: existing.status,
                updatedAt = NOW,
            )
            rows[idx] = updated
            return updated
        }
        override fun delete(id: UUID) {
            if (!rows.removeIf { it.id == id }) {
                throw forge.control.repo.RepositoryException.NotFound("deployment", id)
            }
        }
    }

    private class FakeAudit : AuditRepository {
        val entries = mutableListOf<AuditEntry>()
        override fun append(
            entityType: String,
            entityId: UUID,
            action: String,
            actor: String,
            detailJson: String,
        ): AuditEntry = AuditEntry(UUID.randomUUID(), entityType, entityId, action, actor, NOW, detailJson).also(entries::add)

        override fun listByEntity(entityType: String, entityId: UUID): List<AuditEntry> =
            entries.filter { it.entityType == entityType && it.entityId == entityId }
    }

    private companion object {
        val NOW: Instant = Instant.parse("2026-01-01T00:00:00Z")
    }
}
