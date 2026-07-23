package forge.control.manageddb

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertTrue
import kotlinx.serialization.json.Json
import org.junit.jupiter.api.Assumptions.assumeTrue

class ManagedDbOpenApiContractTest {
    private fun openApiYaml(): String? {
        val root = System.getenv("FORGE_ROOT")?.let { Path.of(it) }
            ?: Path.of("").toAbsolutePath().let { cwd ->
                generateSequence(cwd) { it.parent }.firstOrNull { Files.exists(it.resolve("contracts")) }
            }
            ?: return null
        val path = root.resolve("contracts/openapi/forge-control.openapi.yaml")
        if (!Files.exists(path)) return null
        return Files.readString(path)
    }

    @Test
    fun openApiDeclaresManagedDbPaths() {
        val yaml = openApiYaml()
        assumeTrue(yaml != null, "contracts/ not available in this build context")
        assertTrue(yaml!!.contains("/v1/databases/instances"))
        assertTrue(yaml.contains("x-create-db-instance") || yaml.contains("createDbInstance"))
        assertTrue(yaml.contains("x-get-db-instance") || yaml.contains("getDbInstance"))
        assertTrue(yaml.contains("DbInstance:"))
        assertTrue(yaml.contains("DbDatabase:"))
        assertTrue(yaml.contains("provisioning"))
        assertTrue(yaml.contains("available"))
        assertTrue(yaml.contains("deletionProtection") || yaml.contains("deletion_protection"))
        assertTrue(yaml.contains("x-create-db-database") || yaml.contains("createDbDatabase"))
        assertTrue(yaml.contains("x-get-db-database") || yaml.contains("getDbDatabase"))
        assertTrue(yaml.contains("secretRef"))
        assertTrue(yaml.contains("/v1/databases/{databaseId}"))
    }

    @Test
    fun listExampleOmitsPassword() {
        val example = """
            {
              "id": "33333333-3333-3333-3333-333333333333",
              "instanceId": "11111111-1111-1111-1111-111111111111",
              "name": "appdb",
              "status": "available",
              "host": "127.0.0.1",
              "port": 5433,
              "secretRef": "secret:project/22222222-2222-2222-2222-222222222222/env/managed-db/name/x",
              "username": "appdb_user",
              "createdAt": "2026-07-23T10:00:00Z"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true; explicitNulls = false }
            .decodeFromString(DbDatabaseResponse.serializer(), example)
        assertTrue(decoded.password == null)
        assertTrue(decoded.secretRef!!.startsWith("secret:"))
    }

    @Test
    fun exampleResponseMatchesDtoShape() {
        val example = """
            {
              "id": "11111111-1111-1111-1111-111111111111",
              "projectId": "22222222-2222-2222-2222-222222222222",
              "name": "main",
              "status": "available",
              "engine": "postgres",
              "deletionProtection": true,
              "endpointRef": "fake://managed-db/11111111-1111-1111-1111-111111111111",
              "createdAt": "2026-07-23T10:00:00Z",
              "updatedAt": "2026-07-23T10:00:01Z"
            }
        """.trimIndent()
        val decoded = Json { ignoreUnknownKeys = true }
            .decodeFromString(DbInstanceResponse.serializer(), example)
        assertTrue(decoded.name == "main")
        assertTrue(decoded.status == "available")
        assertTrue(decoded.deletionProtection)
    }
}
