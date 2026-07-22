package forge.control

import forge.control.domain.Environment
import forge.control.domain.Project
import forge.control.http.dto.CreateEnvironmentRequest
import forge.control.http.dto.CreateProjectRequest
import forge.control.http.dto.EnvironmentResponse
import forge.control.http.dto.ProjectResponse
import forge.control.http.dto.toResponse
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ProjectDtosTest {
    private val json = Json { encodeDefaults = true; explicitNulls = false }
    private val id = UUID.fromString("11111111-1111-1111-1111-111111111111")
    private val now = Instant.parse("2026-01-01T00:00:00Z")

    @Test
    fun createProjectRequestRoundTrip() {
        val encoded = json.encodeToString(CreateProjectRequest(name = "acme", slug = "acme"))
        val decoded = json.decodeFromString<CreateProjectRequest>(encoded)
        assertEquals("acme", decoded.name)
        assertEquals("acme", decoded.slug)
    }

    @Test
    fun createProjectRequestAllowsOmittedSlug() {
        val decoded = json.decodeFromString<CreateProjectRequest>("""{"name":"acme"}""")
        assertEquals("acme", decoded.name)
        assertNull(decoded.slug)
    }

    @Test
    fun projectResponseFromDomain() {
        val project = Project(id, "acme", "acme", now, now)
        val response = project.toResponse()
        assertEquals(id.toString(), response.id)
        assertEquals("acme", response.name)
        assertEquals("acme", response.slug)
        assertEquals(now.toString(), response.createdAt)

        val roundTrip = json.decodeFromString<ProjectResponse>(json.encodeToString(response))
        assertEquals(response, roundTrip)
    }

    @Test
    fun environmentDtosRoundTrip() {
        val req = json.decodeFromString<CreateEnvironmentRequest>("""{"name":"development"}""")
        assertEquals("development", req.name)

        val env = Environment(id, id, "development", now, now).toResponse()
        val roundTrip = json.decodeFromString<EnvironmentResponse>(json.encodeToString(env))
        assertEquals(env, roundTrip)
        assertEquals(id.toString(), roundTrip.projectId)
    }
}
