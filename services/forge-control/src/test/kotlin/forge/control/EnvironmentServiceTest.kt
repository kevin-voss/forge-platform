package forge.control

import forge.control.domain.AuditEntry
import forge.control.domain.Environment
import forge.control.domain.Project
import forge.control.http.ApiException
import forge.control.repo.AuditRepository
import forge.control.repo.EnvironmentRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import forge.control.service.EnvironmentService
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class EnvironmentServiceTest {
    private val projectId = UUID.fromString("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

    @Test
    fun createUnderProjectWritesAudit() {
        val projects = FakeProjectRepository(seedProject())
        val envs = FakeEnvironmentRepository()
        val audit = FakeAuditRepository()
        val service = EnvironmentService(projects, envs, audit, actor = "dev")

        val created = service.create(projectId, "development")
        assertEquals("development", created.name)
        assertEquals(projectId, created.projectId)
        assertEquals(1, audit.entries.size)
        assertEquals("environment", audit.entries.single().entityType)
    }

    @Test
    fun unknownProjectReturns404() {
        val service = EnvironmentService(
            FakeProjectRepository(),
            FakeEnvironmentRepository(),
            FakeAuditRepository(),
        )
        assertFailsWith<ApiException.NotFound> {
            service.create(UUID.randomUUID(), "development")
        }
        assertFailsWith<ApiException.NotFound> {
            service.list(UUID.randomUUID())
        }
    }

    @Test
    fun duplicateNameConflict() {
        val projects = FakeProjectRepository(seedProject())
        val service = EnvironmentService(projects, FakeEnvironmentRepository(), FakeAuditRepository())
        service.create(projectId, "development")
        assertFailsWith<ApiException.Conflict> {
            service.create(projectId, "development")
        }
    }

    @Test
    fun blankNameRejected() {
        val projects = FakeProjectRepository(seedProject())
        val service = EnvironmentService(projects, FakeEnvironmentRepository(), FakeAuditRepository())
        assertFailsWith<ApiException.BadRequest> { service.create(projectId, "  ") }
        assertFailsWith<ApiException.BadRequest> { service.create(projectId, null) }
    }

    private fun seedProject(): Project =
        Project(projectId, "Acme", "acme", FIXED_NOW, FIXED_NOW)

    private class FakeProjectRepository(
        private vararg val seeded: Project,
    ) : ProjectRepository {
        private val byId = seeded.associateBy { it.id }.toMutableMap()

        override fun create(name: String, slug: String): Project = error("not used")
        override fun findById(id: UUID): Project? = byId[id]
        override fun list(): List<Project> = byId.values.toList()
        override fun update(id: UUID, name: String?, slug: String?): Project = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class FakeEnvironmentRepository : EnvironmentRepository {
        private val byId = linkedMapOf<UUID, Environment>()

        override fun create(projectId: UUID, name: String): Environment {
            if (byId.values.any { it.projectId == projectId && it.name == name }) {
                throw RepositoryException.Conflict("unique constraint violated")
            }
            val env = Environment(UUID.randomUUID(), projectId, name, FIXED_NOW, FIXED_NOW)
            byId[env.id] = env
            return env
        }

        override fun findById(id: UUID): Environment? = byId[id]

        override fun list(projectId: UUID): List<Environment> =
            byId.values.filter { it.projectId == projectId }

        override fun update(id: UUID, name: String): Environment = error("not used")
        override fun delete(id: UUID) = error("not used")
    }

    private class FakeAuditRepository : AuditRepository {
        val entries = mutableListOf<AuditEntry>()

        override fun append(
            entityType: String,
            entityId: UUID,
            action: String,
            actor: String,
            detailJson: String,
        ): AuditEntry {
            val entry = AuditEntry(
                id = UUID.randomUUID(),
                entityType = entityType,
                entityId = entityId,
                action = action,
                actor = actor,
                at = FIXED_NOW,
                detailJson = detailJson,
            )
            entries += entry
            return entry
        }

        override fun listByEntity(entityType: String, entityId: UUID): List<AuditEntry> =
            entries.filter { it.entityType == entityType && it.entityId == entityId }
    }

    companion object {
        private val FIXED_NOW: Instant = Instant.parse("2026-01-01T00:00:00Z")
    }
}
