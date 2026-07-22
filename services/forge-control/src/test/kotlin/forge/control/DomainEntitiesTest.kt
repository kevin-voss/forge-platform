package forge.control

import forge.control.domain.Application
import forge.control.domain.Deployment
import forge.control.domain.Environment
import forge.control.domain.Project
import forge.control.domain.Service
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertFailsWith

class DomainEntitiesTest {
    private val now = Instant.parse("2026-01-01T00:00:00Z")
    private val id = UUID.fromString("11111111-1111-1111-1111-111111111111")

    @Test
    fun projectRejectsBlankName() {
        assertFailsWith<IllegalArgumentException> {
            Project(id, "  ", "slug", now, now)
        }
    }

    @Test
    fun projectRejectsBlankSlug() {
        assertFailsWith<IllegalArgumentException> {
            Project(id, "name", "", now, now)
        }
    }

    @Test
    fun environmentRejectsBlankName() {
        assertFailsWith<IllegalArgumentException> {
            Environment(id, id, " ", now, now)
        }
    }

    @Test
    fun applicationRejectsBlankName() {
        assertFailsWith<IllegalArgumentException> {
            Application(id, id, "", now, now)
        }
    }

    @Test
    fun serviceRejectsInvalidPort() {
        assertFailsWith<IllegalArgumentException> {
            Service(id, id, "api", 0, now, now)
        }
        assertFailsWith<IllegalArgumentException> {
            Service(id, id, "api", 70000, now, now)
        }
    }

    @Test
    fun deploymentRejectsNegativeReplicas() {
        assertFailsWith<IllegalArgumentException> {
            Deployment(id, id, id, "img:latest", -1, "pending", now, now)
        }
    }
}
