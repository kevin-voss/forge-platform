package forge.identity

import forge.identity.health.IdentityResponse
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assumptions.assumeTrue

class OpenApiContractTest {
    private fun openApiYaml(): String? {
        val root = System.getenv("FORGE_ROOT")?.let { Path.of(it) }
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.firstOrNull { Files.exists(it.resolve("contracts")) }
            }
            ?: return null
        val path = root.resolve("contracts/openapi/forge-identity.openapi.yaml")
        if (!Files.exists(path)) return null
        return Files.readString(path)
    }

    @Test
    fun openApiDeclaresContractPaths() {
        val yaml = openApiYaml()
        assumeTrue(yaml != null, "contracts/ not available in this build context")
        assertTrue(yaml!!.contains("/health/live"))
        assertTrue(yaml.contains("/health/ready"))
        assertTrue(yaml.contains("getIdentity") || yaml.contains("operationId: getIdentity"))
        assertTrue(yaml.contains("forge-identity"))
        assertTrue(yaml.contains("kotlin"))
        assertTrue(yaml.contains("Identity:"))
        assertTrue(yaml.contains("service:"))
        assertTrue(yaml.contains("language:"))
        assertTrue(yaml.contains("status:"))
        assertTrue(yaml.contains("/v1/users"))
        assertTrue(yaml.contains("/v1/orgs"))
        assertTrue(yaml.contains("/v1/projects"))
        assertTrue(yaml.contains("/v1/users/{userId}/memberships"))
        assertTrue(yaml.contains("/v1/orgs/{orgId}/members"))
        assertTrue(yaml.contains("/v1/projects/{projectId}/members"))
        assertTrue(yaml.contains("ErrorEnvelope:"))
        assertTrue(yaml.contains("createUser") || yaml.contains("operationId: createUser"))
        assertTrue(yaml.contains("createOrg") || yaml.contains("operationId: createOrg"))
        assertTrue(yaml.contains("addProjectMember") || yaml.contains("operationId: addProjectMember"))
        assertTrue(yaml.contains("display_name"))
        assertTrue(yaml.contains("user_id"))
    }

    @Test
    fun errorEnvelopeExampleValidates() {
        val example = """
            {
              "error": {
                "code": "conflict",
                "message": "email already registered",
                "details": { "email": "dev@x.com" },
                "requestId": "req_example"
              }
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(forge.identity.http.ErrorEnvelope.serializer(), example)
        assertEquals("conflict", decoded.error.code)
        assertEquals("req_example", decoded.error.requestId)
        assertEquals("dev@x.com", decoded.error.details?.get("email"))
    }

    @Test
    fun identityExampleMatchesRuntimeContractShape() {
        val example = """
            {
              "service": "forge-identity",
              "language": "kotlin",
              "status": "running",
              "version": "0.1.0",
              "uptime_seconds": 12.5
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(IdentityResponse.serializer(), example)
        assertEquals("forge-identity", decoded.service)
        assertEquals("kotlin", decoded.language)
        assertEquals("running", decoded.status)
        assertEquals("0.1.0", decoded.version)
        assertEquals(12.5, decoded.uptime_seconds)
    }
}
