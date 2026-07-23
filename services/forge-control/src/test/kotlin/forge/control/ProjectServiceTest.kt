package forge.control

import forge.control.domain.AuditEntry
import forge.control.domain.Project
import forge.control.http.ApiException
import forge.control.repo.AuditRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import forge.control.service.ProjectService
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class ProjectServiceTest {
    @Test
    fun createDerivesSlugAndWritesAudit() {
        val projects = FakeProjectRepository()
        val audit = FakeAuditRepository()
        val service = ProjectService(projects, audit, actor = "dev")

        val created = service.create("Acme Corp", null)
        assertEquals("Acme Corp", created.name)
        assertEquals("acme-corp", created.slug)
        assertEquals(1, audit.entries.size)
        assertEquals("create", audit.entries.single().action)
        assertEquals("dev", audit.entries.single().actor)
        assertEquals("project", audit.entries.single().entityType)
    }

    @Test
    fun createUsesExplicitSlug() {
        val service = ProjectService(FakeProjectRepository(), FakeAuditRepository())
        val created = service.create("Acme", "custom-slug")
        assertEquals("custom-slug", created.slug)
    }

    @Test
    fun blankNameRejected() {
        val service = ProjectService(FakeProjectRepository(), FakeAuditRepository())
        assertFailsWith<ApiException.BadRequest> { service.create("  ", null) }
        assertFailsWith<ApiException.BadRequest> { service.create(null, null) }
    }

    @Test
    fun duplicateSlugConflict() {
        val projects = FakeProjectRepository()
        val service = ProjectService(projects, FakeAuditRepository())
        service.create("A", "taken")
        val ex = assertFailsWith<ApiException.Conflict> { service.create("B", "taken") }
        assertEquals("conflict", ex.code)
    }

    @Test
    fun getUnknownReturnsNotFound() {
        val service = ProjectService(FakeProjectRepository(), FakeAuditRepository())
        assertFailsWith<ApiException.NotFound> { service.get(UUID.randomUUID()) }
    }

    @Test
    fun listReturnsCreated() {
        val service = ProjectService(FakeProjectRepository(), FakeAuditRepository())
        service.create("One", "one")
        service.create("Two", "two")
        assertEquals(2, service.list().size)
    }

    @Test
    fun invalidExplicitSlugRejected() {
        val service = ProjectService(FakeProjectRepository(), FakeAuditRepository())
        assertFailsWith<ApiException.BadRequest> { service.create("Acme", "BAD_SLUG") }
    }

    private class FakeProjectRepository : ProjectRepository {
        private val byId = linkedMapOf<UUID, Project>()
        private val bySlug = mutableMapOf<String, UUID>()

        override fun create(name: String, slug: String): Project {
            if (bySlug.containsKey(slug)) {
                throw RepositoryException.Conflict("unique constraint violated: slug=$slug")
            }
            val id = UUID.randomUUID()
            val project = Project(id, name, slug, FIXED_NOW, FIXED_NOW)
            byId[id] = project
            bySlug[slug] = id
            return project
        }

        override fun findById(id: UUID): Project? = byId[id]
        override fun findBySlug(slug: String): Project? = null

        override fun list(): List<Project> = byId.values.toList()

        override fun update(id: UUID, name: String?, slug: String?): Project =
            error("not used")

        override fun delete(id: UUID) {
            error("not used")
        }
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
