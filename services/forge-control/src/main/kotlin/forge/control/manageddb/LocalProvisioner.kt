package forge.control.manageddb

import forge.control.logging.JsonLog
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/**
 * Real local provisioner: one Postgres container per instance on the managed
 * Docker network, with least-privilege databases/roles inside.
 */
class LocalProvisioner(
    private val isolation: IsolationGuard,
    private val docker: DockerEngine = CliDockerEngine(),
    private val network: String = "forge-net",
    private val image: String = "postgres:16",
    private val endpointHost: String = "127.0.0.1",
    private val readyTimeoutMs: Long = 60_000,
    private val log: JsonLog? = null,
    private val adminFactory: (host: String, port: Int, password: String) -> PostgresAdminClient =
        { host, port, password -> PostgresAdmin(host, port, adminPassword = password) },
) : Provisioner {
    private val endpoints = ConcurrentHashMap<UUID, InstanceEndpoint>()

    override fun createInstance(instanceId: UUID, projectId: UUID, name: String): ProvisionResult {
        val containerName = containerName(instanceId)
        val adminPassword = CredentialGenerator.password(32)
        var containerId: String? = null
        return try {
            log?.info(
                "managed db starting container",
                "instance_id" to instanceId,
                "container_name" to containerName,
                "network" to network,
                "image" to image,
            )
            val info = docker.createAndStartPostgres(
                name = containerName,
                network = network,
                image = image,
                adminPassword = adminPassword,
                labels = mapOf(
                    "forge.managed_db" to "true",
                    "forge.instance_id" to instanceId.toString(),
                    "forge.project_id" to projectId.toString(),
                ),
            )
            containerId = info.id
            val port = docker.publishedPort(info.id)
            val admin = adminFactory(endpointHost, port, adminPassword)
            admin.waitReady(readyTimeoutMs)
            admin.ping("postgres", adminPassword, "postgres")
            val endpointRef = "postgres://$endpointHost:$port/postgres"
            isolation.assertNotControlDatabase(endpointRef)
            val endpoint = InstanceEndpoint(
                endpointRef = endpointRef,
                host = endpointHost,
                port = port,
                containerId = info.id,
            )
            endpoints[instanceId] = endpoint
            log?.info(
                "managed db container ready",
                "instance_id" to instanceId,
                "host" to endpointHost,
                "port" to port,
                "container_id" to info.id.take(12),
            )
            ProvisionResult(
                endpointRef = endpointRef,
                detail = "local-create-instance",
                host = endpointHost,
                port = port,
                containerId = info.id,
            )
        } catch (e: Exception) {
            containerId?.let { docker.removeContainer(it) }
            docker.removeContainer(containerName)
            endpoints.remove(instanceId)
            throw ProvisionerException(
                "local provisioner failed to create instance: ${e.message ?: e.javaClass.simpleName}",
                e,
            )
        }
    }

    override fun deleteInstance(instanceId: UUID) {
        val name = containerName(instanceId)
        val cached = endpoints.remove(instanceId)
        cached?.containerId?.let { docker.removeContainer(it) }
        docker.removeContainer(name)
    }

    override fun createDatabase(instanceId: UUID, databaseName: String): ProvisionResult {
        // Role creation happens in createRole with credentials; this validates connectivity.
        val endpoint = requireEndpoint(instanceId)
        val adminPassword = adminPasswordFor(endpoint.containerId!!)
        val admin = adminFactory(endpoint.host, endpoint.port, adminPassword)
        admin.ping("postgres", adminPassword, "postgres")
        isolation.assertNotControlDatabase(endpoint.endpointRef)
        return ProvisionResult(
            endpointRef = "postgres://${endpoint.host}:${endpoint.port}/$databaseName",
            detail = "local-create-database-pending-role",
            host = endpoint.host,
            port = endpoint.port,
            containerId = endpoint.containerId,
        )
    }

    override fun createRole(databaseId: UUID, username: String): ProvisionResult {
        throw UnsupportedOperationException(
            "LocalProvisioner.createRole requires createDatabaseWithRole; use ManagedDbService orchestration",
        )
    }

    override fun createDatabaseWithRole(
        instanceId: UUID,
        databaseName: String,
        username: String,
        password: String,
    ): ProvisionResult {
        PostgresAdmin.validateIdent(databaseName, "database")
        PostgresAdmin.validateIdent(username, "role")
        val endpoint = requireEndpoint(instanceId)
        val adminPassword = adminPasswordFor(endpoint.containerId!!)
        val admin = adminFactory(endpoint.host, endpoint.port, adminPassword)
        var created = false
        return try {
            log?.info(
                "managed db creating database and role",
                "instance_id" to instanceId,
                "database" to databaseName,
                "username" to username,
            )
            admin.createDatabaseAndRole(databaseName, username, password)
            created = true
            if (!admin.ping(username, password, databaseName)) {
                throw ProvisionerException("health check failed after role create")
            }
            val endpointRef = "postgres://${endpoint.host}:${endpoint.port}/$databaseName"
            isolation.assertNotControlDatabase(endpointRef)
            ProvisionResult(
                endpointRef = endpointRef,
                detail = "local-create-database-role",
                host = endpoint.host,
                port = endpoint.port,
                containerId = endpoint.containerId,
                username = username,
                password = password,
            )
        } catch (e: Exception) {
            if (created) {
                admin.dropDatabaseAndRole(databaseName, username)
            }
            throw ProvisionerException(
                "local provisioner failed to create database/role: ${e.message ?: e.javaClass.simpleName}",
                e,
            )
        }
    }

    override fun dumpDatabase(instanceId: UUID, databaseName: String): DumpArchive {
        PostgresAdmin.validateIdent(databaseName, "database")
        val endpoint = requireEndpoint(instanceId)
        val containerId = endpoint.containerId
            ?: throw ProvisionerException("instance has no container_id for dump")
        isolation.assertNotControlDatabase(endpoint.endpointRef)
        log?.info(
            "managed db dump starting",
            "instance_id" to instanceId,
            "database" to databaseName,
            "container_id" to containerId.take(12),
        )
        return try {
            // Consistent snapshot via pg_dump custom format (default transaction snapshot).
            val bytes = docker.exec(
                containerId,
                listOf(
                    "pg_dump",
                    "-U", "postgres",
                    "--format=custom",
                    "--serializable-deferrable",
                    "-d", databaseName,
                ),
            )
            if (bytes.isEmpty()) {
                throw ProvisionerException("pg_dump returned empty archive")
            }
            val checksum = BackupChecksum.sha256Hex(bytes)
            log?.info(
                "managed db dump complete",
                "instance_id" to instanceId,
                "database" to databaseName,
                "size_bytes" to bytes.size,
                "checksum" to checksum,
            )
            DumpArchive(bytes = bytes, checksum = checksum, sizeBytes = bytes.size.toLong())
        } catch (e: Exception) {
            throw ProvisionerException(
                "local provisioner dump failed: ${e.message ?: e.javaClass.simpleName}",
                e,
            )
        }
    }

    override fun restoreDatabase(instanceId: UUID, databaseName: String, archive: ByteArray) {
        PostgresAdmin.validateIdent(databaseName, "database")
        if (archive.isEmpty()) {
            throw ProvisionerException("restore refused empty archive")
        }
        val endpoint = requireEndpoint(instanceId)
        val containerId = endpoint.containerId
            ?: throw ProvisionerException("instance has no container_id for restore")
        isolation.assertNotControlDatabase(endpoint.endpointRef)
        log?.info(
            "managed db restore starting",
            "instance_id" to instanceId,
            "database" to databaseName,
            "size_bytes" to archive.size,
            "container_id" to containerId.take(12),
        )
        try {
            docker.exec(
                containerId,
                listOf(
                    "pg_restore",
                    "-U", "postgres",
                    "--clean",
                    "--if-exists",
                    "--no-privileges",
                    "--dbname", databaseName,
                ),
                stdin = archive,
            )
            log?.info(
                "managed db restore complete",
                "instance_id" to instanceId,
                "database" to databaseName,
            )
        } catch (e: Exception) {
            throw ProvisionerException(
                "local provisioner restore failed: ${e.message ?: e.javaClass.simpleName}",
                e,
            )
        }
    }

    override fun rotateCredential(credentialId: UUID): ProvisionResult {
        throw UnsupportedOperationException("rotate reserved for 18.05")
    }

    /** Rehydrate endpoint cache after Control restart (from persisted host/port/container_id). */
    fun rememberEndpoint(instanceId: UUID, endpoint: InstanceEndpoint) {
        endpoints[instanceId] = endpoint
    }

    private fun requireEndpoint(instanceId: UUID): InstanceEndpoint =
        endpoints[instanceId]
            ?: throw ProvisionerException("instance endpoint not cached; was the container provisioned?")

    private fun adminPasswordFor(containerId: String): String {
        val env = docker.containerEnv(containerId)
        return env["POSTGRES_PASSWORD"]
            ?: throw ProvisionerException("POSTGRES_PASSWORD missing from container env")
    }

    companion object {
        fun containerName(instanceId: UUID): String =
            "forge-mdb-${instanceId.toString().replace("-", "").take(12)}"
    }
}

data class InstanceEndpoint(
    val endpointRef: String,
    val host: String,
    val port: Int,
    val containerId: String?,
)

class ProvisionerException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)
