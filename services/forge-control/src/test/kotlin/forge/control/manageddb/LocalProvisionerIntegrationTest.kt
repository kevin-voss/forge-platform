package forge.control.manageddb

import forge.control.logging.JsonLog
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.TestInstance
import java.sql.DriverManager
import java.util.UUID
import kotlin.test.assertFails
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * LocalProvisioner against real Docker. Skipped when Docker is unavailable.
 * Excluded from default `test` task (name matches *IntegrationTest*).
 */
@TestInstance(TestInstance.Lifecycle.PER_METHOD)
class LocalProvisionerIntegrationTest {
    private val network = "forge-mdb-test-net"
    private val controlJdbc = System.getenv("DATABASE_URL")
        ?: "jdbc:postgresql://127.0.0.1:5001/forge"
    private val controlUser = System.getenv("DATABASE_USER") ?: "forge"
    private val created = mutableListOf<UUID>()
    private val docker = CliDockerEngine()
    private val isolation = IsolationGuard(controlJdbc, controlUser)
    private val provisioner = LocalProvisioner(
        isolation = isolation,
        docker = docker,
        network = network,
        image = System.getenv("FORGE_DB_POSTGRES_IMAGE")?.trim()?.ifEmpty { null } ?: "postgres:16",
        endpointHost = "127.0.0.1",
        log = JsonLog("forge-control-test", "info"),
    )

    @AfterEach
    fun cleanup() {
        for (id in created) {
            try {
                provisioner.deleteInstance(id)
            } catch (_: Exception) {
                // best effort
            }
        }
        created.clear()
    }

    @Test
    fun provisionsIsolatedInstanceDatabaseAndCredentials() {
        assumeTrue(dockerAvailable(), "docker not available")
        val a = UUID.randomUUID()
        val b = UUID.randomUUID()
        created += a
        created += b

        val instA = provisioner.createInstance(a, UUID.randomUUID(), "main-a")
        assertNotNull(instA.host)
        assertNotNull(instA.port)
        assertNotNull(instA.containerId)
        assertTrue(!isolation.isControlDatabase(instA.endpointRef))
        assertTrue(!instA.endpointRef.contains("5001/forge"))

        val passwordA = CredentialGenerator.password()
        val userA = "appdb_a_user"
        val dbA = provisioner.createDatabaseWithRole(a, "appdb_a", userA, passwordA)
        val adminPw = docker.containerEnv(instA.containerId!!)["POSTGRES_PASSWORD"]!!
        val admin = PostgresAdmin(dbA.host!!, dbA.port!!, adminPassword = adminPw)
        assertTrue(admin.ping(userA, passwordA, "appdb_a"))

        val instB = provisioner.createInstance(b, UUID.randomUUID(), "main-b")
        val passwordB = CredentialGenerator.password()
        val userB = "appdb_b_user"
        val dbB = provisioner.createDatabaseWithRole(b, "appdb_b", userB, passwordB)

        // Cross-instance: A's credentials cannot authenticate against B's database.
        assertFails {
            DriverManager.getConnection(
                "jdbc:postgresql://${dbB.host}:${dbB.port}/appdb_b",
                userA,
                passwordA,
            ).use { }
        }
        assertNotEquals(instA.containerId, instB.containerId)

        // Control DB remains distinct / unreferenced as a product endpoint.
        assertTrue(isolation.isControlDatabase(controlJdbc))
        assertTrue(!isolation.isControlDatabase(instA.endpointRef))
        assertTrue(!isolation.isControlDatabase(dbA.endpointRef))
    }

    @Test
    fun badImageFailsAndCleansUp() {
        assumeTrue(dockerAvailable(), "docker not available")
        val bad = LocalProvisioner(
            isolation = isolation,
            docker = docker,
            network = network,
            image = "forge/definitely-not-a-real-postgres-image:0.0.0",
            endpointHost = "127.0.0.1",
            log = JsonLog("forge-control-test", "error"),
        )
        val id = UUID.randomUUID()
        val name = LocalProvisioner.containerName(id)
        assertFails {
            bad.createInstance(id, UUID.randomUUID(), "boom")
        }
        assertTrue(!docker.containerRunning(name))
    }

    private fun dockerAvailable(): Boolean =
        try {
            val p = ProcessBuilder("docker", "info").start()
            p.waitFor() == 0
        } catch (_: Exception) {
            false
        }
}
