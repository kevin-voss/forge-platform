package forge.control

import forge.control.domain.Application
import forge.control.domain.AuditEntry
import forge.control.domain.Project
import forge.control.domain.Service
import forge.control.http.ApiException
import forge.control.repo.ApplicationRepository
import forge.control.repo.AuditRepository
import forge.control.repo.ProjectRepository
import forge.control.repo.RepositoryException
import forge.control.repo.ServiceRepository
import forge.control.service.RelationshipValidator
import forge.control.service.ServiceService
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class ServiceImageTest {
    private val now = Instant.parse("2026-07-22T12:00:00Z")
    private val serviceId = UUID.randomUUID()
    private val applicationId = UUID.randomUUID()

    @Test
    fun recordImageUpdatesServiceAndAudits() {
        val repo = MutableServiceRepo(
            Service(serviceId, applicationId, "api", 8080, now, now),
        )
        val audit = RecordingAudit()
        val svc = ServiceService(repo, noopRelationships(), audit, actor = "dev")

        val updated = svc.recordImage(
            serviceId,
            " localhost:5000/acme-api:abc-1 ",
            "sha256:deadbeef",
            "abc1234",
            "11111111-1111-4111-8111-111111111111",
        )

        assertEquals("localhost:5000/acme-api:abc-1", updated.image)
        assertEquals("sha256:deadbeef", updated.imageDigest)
        assertEquals("abc1234", updated.imageCommit)
        assertEquals("11111111-1111-4111-8111-111111111111", updated.imageBuildId)
        assertEquals(1, audit.actions.size)
        assertEquals("record_image", audit.actions.single())
    }

    @Test
    fun recordImageRejectsBlankImage() {
        val repo = MutableServiceRepo(
            Service(serviceId, applicationId, "api", 8080, now, now),
        )
        val svc = ServiceService(repo, noopRelationships(), RecordingAudit(), actor = "dev")
        assertFailsWith<ApiException.BadRequest> {
            svc.recordImage(serviceId, "  ", null, null, null)
        }
        assertNull(repo.current.image)
    }

    @Test
    fun recordImageMissingServiceIsNotFound() {
        val svc = ServiceService(MutableServiceRepo(null), noopRelationships(), RecordingAudit(), actor = "dev")
        assertFailsWith<ApiException.NotFound> {
            svc.recordImage(serviceId, "localhost:5000/x:1", null, null, null)
        }
    }

    private fun noopRelationships() =
        RelationshipValidator(
            object : ProjectRepository {
                override fun create(name: String, slug: String): Project = error("not used")
                override fun findById(id: UUID): Project? = null
        override fun findBySlug(slug: String): Project? = null
                override fun list(): List<Project> = emptyList()
                override fun update(id: UUID, name: String?, slug: String?): Project = error("not used")
                override fun delete(id: UUID) = error("not used")
            },
            object : ApplicationRepository {
                override fun create(projectId: UUID, name: String): Application = error("not used")
                override fun findById(id: UUID): Application? = null
        override fun findByProjectAndName(projectId: UUID, name: String): Application? = null
                override fun list(projectId: UUID): List<Application> = emptyList()
                override fun update(id: UUID, name: String): Application = error("not used")
                override fun delete(id: UUID) = error("not used")
            },
        )

    private class MutableServiceRepo(initial: Service?) : ServiceRepository {
        var current: Service = initial ?: Service(
            UUID.randomUUID(),
            UUID.randomUUID(),
            "missing",
            8080,
            Instant.now(),
            Instant.now(),
        )
        private val present = initial != null

        override fun create(applicationId: UUID, name: String, port: Int): Service = error("not used")
        override fun findById(id: UUID): Service? = if (present && current.id == id) current else null
        override fun findByApplicationAndName(applicationId: UUID, name: String): Service? = null
        override fun list(applicationId: UUID): List<Service> = error("not used")
        override fun update(id: UUID, name: String?, port: Int?): Service = error("not used")
        override fun recordImage(
            id: UUID,
            image: String,
            digest: String?,
            commit: String?,
            buildId: String?,
        ): Service {
            val existing = findById(id) ?: throw RepositoryException.NotFound("service", id)
            current = existing.copy(
                image = image,
                imageDigest = digest,
                imageCommit = commit,
                imageBuildId = buildId,
                updatedAt = Instant.now(),
            )
            return current
        }

        override fun delete(id: UUID) = error("not used")
    }

    private class RecordingAudit : AuditRepository {
        val actions = mutableListOf<String>()
        override fun append(
            entityType: String,
            entityId: UUID,
            action: String,
            actor: String,
            detailJson: String,
        ): AuditEntry {
            actions += action
            return AuditEntry(UUID.randomUUID(), entityType, entityId, action, actor, Instant.now(), detailJson)
        }

        override fun listByEntity(entityType: String, entityId: UUID): List<AuditEntry> = emptyList()
    }
}
