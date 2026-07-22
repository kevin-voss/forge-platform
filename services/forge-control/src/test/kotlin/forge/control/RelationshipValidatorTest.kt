package forge.control

import forge.control.domain.Application
import forge.control.domain.Project
import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.ProjectRepository
import forge.control.service.ProjectService
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class RelationshipValidatorTest {
    private val projectId = UUID.randomUUID()
    private val applicationId = UUID.randomUUID()

    @Test
    fun nameAndPortValidationAreTableDriven() {
        listOf(null, "", "  ", "x".repeat(129)).forEach { name ->
            assertFailsWith<ApiException.BadRequest> { ProjectService.validateName(name) }
        }
        assertEquals("web", ProjectService.validateName(" web "))

        listOf<Int?>(null, -1, 0, 65536).forEach { port ->
            assertFailsWith<ApiException.BadRequest> { ServiceService.validatePort(port) }
        }
        assertEquals(1, ServiceService.validatePort(1))
        assertEquals(65535, ServiceService.validatePort(65535))
    }

    @Test
    fun relationshipValidatorAcceptsExistingParentsAndRejectsUnknownParents() {
        val validator = RelationshipValidator(
            FakeProjectRepository(projectId),
            FakeApplicationRepository(applicationId, projectId),
        )
        validator.requireProject(projectId)
        validator.requireApplication(applicationId)

        assertFailsWith<ApiException.NotFound> { validator.requireProject(UUID.randomUUID()) }
        assertFailsWith<ApiException.NotFound> { validator.requireApplication(UUID.randomUUID()) }
    }

    private class FakeProjectRepository(existingId: UUID) : ProjectRepository {
        private val project = Project(existingId, "Acme", "acme", NOW, NOW)
        override fun create(name: String, slug: String): Project = error("not used")
        override fun findById(id: UUID): Project? = project.takeIf { it.id == id }
        override fun list(): List<Project> = listOf(project)
        override fun update(id: UUID, name: String?, slug: String?): Project = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class FakeApplicationRepository(
        existingId: UUID,
        projectId: UUID,
    ) : ApplicationRepository {
        private val application = Application(existingId, projectId, "web", NOW, NOW)
        override fun create(projectId: UUID, name: String): Application = error("not used")
        override fun findById(id: UUID): Application? = application.takeIf { it.id == id }
        override fun list(projectId: UUID): List<Application> = listOf(application)
        override fun update(id: UUID, name: String): Application = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private companion object {
        val NOW: Instant = Instant.parse("2026-01-01T00:00:00Z")
    }
}
