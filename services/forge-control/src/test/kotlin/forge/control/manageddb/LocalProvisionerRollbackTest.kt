package forge.control.manageddb

import forge.control.logging.JsonLog
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class LocalProvisionerRollbackTest {
    @Test
    fun containerStartFailureCleansUpPartialResources() {
        val docker = FailingDockerEngine()
        val isolation = IsolationGuard(
            controlJdbcUrl = "jdbc:postgresql://127.0.0.1:5001/forge",
            controlUser = "forge",
        )
        val provisioner = LocalProvisioner(
            isolation = isolation,
            docker = docker,
            network = "forge-net-test",
            image = "postgres:not-a-real-image-xyz",
            log = JsonLog("forge-control-test", "error"),
        )
        assertFailsWith<ProvisionerException> {
            provisioner.createInstance(UUID.randomUUID(), UUID.randomUUID(), "main")
        }
        assertTrue(docker.removeCalls.get() >= 1, "expected container cleanup on failure")
    }

    @Test
    fun createDatabaseWithRoleRollsBackOnHealthFailure() {
        val isolation = IsolationGuard(
            controlJdbcUrl = "jdbc:postgresql://127.0.0.1:5001/forge",
            controlUser = "forge",
        )
        var dropped = false
        val provisioner = LocalProvisioner(
            isolation = isolation,
            docker = RecordingDockerEngine(),
            adminFactory = { _, _, _ ->
                object : PostgresAdminClient {
                    override fun waitReady(timeoutMs: Long, pollMs: Long) = Unit

                    override fun ping(user: String, password: String, database: String): Boolean {
                        // Admin ping during createDatabase path is unused here; product ping fails.
                        return user == "postgres"
                    }

                    override fun createDatabaseAndRole(
                        databaseName: String,
                        roleName: String,
                        rolePassword: String,
                    ): List<String> = RoleGrantSql.plan(databaseName, roleName)

                    override fun dropDatabaseAndRole(databaseName: String, roleName: String) {
                        dropped = true
                    }
                }
            },
        )
        val instanceId = UUID.randomUUID()
        provisioner.rememberEndpoint(
            instanceId,
            InstanceEndpoint("postgres://127.0.0.1:5999/postgres", "127.0.0.1", 5999, "cid-2"),
        )
        assertFailsWith<ProvisionerException> {
            provisioner.createDatabaseWithRole(
                instanceId,
                "appdb",
                "appdb_user",
                CredentialGenerator.password(),
            )
        }
        assertTrue(dropped, "expected dropDatabaseAndRole on health failure")
    }

    private class FailingDockerEngine : DockerEngine {
        val removeCalls = AtomicInteger(0)

        override fun ensureNetwork(name: String) = Unit

        override fun createAndStartPostgres(
            name: String,
            network: String,
            image: String,
            adminPassword: String,
            labels: Map<String, String>,
        ): ContainerInfo {
            throw DockerEngineException("docker run failed for image '$image': pull access denied")
        }

        override fun removeContainer(idOrName: String) {
            removeCalls.incrementAndGet()
        }

        override fun publishedPort(containerId: String, containerPort: Int): Int = 0

        override fun containerEnv(containerId: String): Map<String, String> = emptyMap()

        override fun containerRunning(containerId: String): Boolean = false
    }

    private class RecordingDockerEngine : DockerEngine {
        override fun ensureNetwork(name: String) = Unit

        override fun createAndStartPostgres(
            name: String,
            network: String,
            image: String,
            adminPassword: String,
            labels: Map<String, String>,
        ): ContainerInfo = ContainerInfo("cid", name)

        override fun removeContainer(idOrName: String) = Unit

        override fun publishedPort(containerId: String, containerPort: Int): Int = 5999

        override fun containerEnv(containerId: String): Map<String, String> =
            mapOf("POSTGRES_PASSWORD" to "admin-secret")

        override fun containerRunning(containerId: String): Boolean = true
    }
}
