package forge.control.manageddb

import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/** Result of a provisioner operation (endpoint refs never equal Control's JDBC URL). */
data class ProvisionResult(
    val endpointRef: String,
    val detail: String? = null,
    val host: String? = null,
    val port: Int? = null,
    val containerId: String? = null,
    /** Product role username when creating a database+role. */
    val username: String? = null,
    /**
     * One-time password from the provisioner. Control must store it in Secrets
     * and must not persist it in management-plane tables or list responses.
     */
    val password: String? = null,
)

/**
 * Seam over real Postgres ops. Control persists management-plane records;
 * the provisioner creates/attaches/backs up product Postgres elsewhere.
 *
 * `fake` (CI) or `local` (Docker containers, step 18.02).
 */
interface Provisioner {
    fun createInstance(instanceId: UUID, projectId: UUID, name: String): ProvisionResult
    fun deleteInstance(instanceId: UUID)
    fun createDatabase(instanceId: UUID, databaseName: String): ProvisionResult
    fun createRole(databaseId: UUID, username: String): ProvisionResult

    /**
     * Create a database and least-privilege role with the given credentials.
     * Implementations must roll back partial resources on failure.
     */
    fun createDatabaseWithRole(
        instanceId: UUID,
        databaseName: String,
        username: String,
        password: String,
    ): ProvisionResult

    /**
     * Capture a consistent dump of [databaseName] on [instanceId]
     * (`pg_dump` / equivalent). Never targets Control's own database.
     */
    fun dumpDatabase(instanceId: UUID, databaseName: String): DumpArchive

    /**
     * Restore [archive] into [databaseName] on [instanceId]
     * (`pg_restore` / equivalent). Caller verifies checksum before invoke.
     */
    fun restoreDatabase(instanceId: UUID, databaseName: String, archive: ByteArray)

    fun rotateCredential(credentialId: UUID): ProvisionResult
}

/**
 * Deterministic no-op provisioner for CI. Never returns Control's own DB URL.
 */
class FakeProvisioner(
    private val isolation: IsolationGuard,
) : Provisioner {
    override fun createInstance(instanceId: UUID, projectId: UUID, name: String): ProvisionResult {
        val endpoint = "fake://managed-db/$instanceId"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(
            endpointRef = endpoint,
            detail = "fake-create-instance",
            host = "fake.local",
            port = 5432,
            containerId = "fake-$instanceId",
        )
    }

    override fun deleteInstance(instanceId: UUID) {
        // no-op
    }

    override fun createDatabase(instanceId: UUID, databaseName: String): ProvisionResult {
        val endpoint = "fake://managed-db/$instanceId/db/$databaseName"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(
            endpointRef = endpoint,
            detail = "fake-create-database",
            host = "fake.local",
            port = 5432,
        )
    }

    override fun createRole(databaseId: UUID, username: String): ProvisionResult {
        val endpoint = "fake://managed-db/role/$databaseId/$username"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-create-role", username = username)
    }

    override fun createDatabaseWithRole(
        instanceId: UUID,
        databaseName: String,
        username: String,
        password: String,
    ): ProvisionResult {
        val endpoint = "fake://managed-db/$instanceId/db/$databaseName"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(
            endpointRef = endpoint,
            detail = "fake-create-database-role",
            host = "fake.local",
            port = 5432,
            containerId = "fake-$instanceId",
            username = username,
            password = password,
        )
    }

    private val dumps = ConcurrentHashMap<String, ByteArray>()

    override fun dumpDatabase(instanceId: UUID, databaseName: String): DumpArchive {
        val endpoint = "fake://managed-db/$instanceId/db/$databaseName"
        isolation.assertNotControlDatabase(endpoint)
        val key = "$instanceId/$databaseName"
        val payload = dumps[key] ?: "FAKE_DUMP:$key:fixture-v1".toByteArray(Charsets.UTF_8)
        val checksum = BackupChecksum.sha256Hex(payload)
        return DumpArchive(bytes = payload, checksum = checksum, sizeBytes = payload.size.toLong())
    }

    override fun restoreDatabase(instanceId: UUID, databaseName: String, archive: ByteArray) {
        val endpoint = "fake://managed-db/$instanceId/db/$databaseName"
        isolation.assertNotControlDatabase(endpoint)
        if (archive.isEmpty()) {
            throw ProvisionerException("fake restore refused empty archive")
        }
        dumps["$instanceId/$databaseName"] = archive
    }

    /** Test helper: seed in-memory fixture bytes that dump will capture. */
    fun seedDump(instanceId: UUID, databaseName: String, payload: ByteArray) {
        dumps["$instanceId/$databaseName"] = payload
    }

    /** Test helper: read last restored/seeded payload. */
    fun currentDump(instanceId: UUID, databaseName: String): ByteArray? =
        dumps["$instanceId/$databaseName"]

    override fun rotateCredential(credentialId: UUID): ProvisionResult {
        val endpoint = "fake://managed-db/rotate/$credentialId"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-rotate")
    }
}

/**
 * Enforces the isolation invariant: managed product DBs must never use
 * Control's own JDBC credentials/connection.
 */
class IsolationGuard(
    controlJdbcUrl: String,
    controlUser: String,
) {
    private val controlNormalized = normalize(controlJdbcUrl)
    private val controlUserNormalized = controlUser.trim().lowercase()

    fun assertNotControlDatabase(endpointOrUrl: String?) {
        if (endpointOrUrl.isNullOrBlank()) return
        val candidate = normalize(endpointOrUrl)
        if (candidate == controlNormalized) {
            throw IsolationViolation(
                "managed database must not use Control's own database connection",
            )
        }
        // Refuse JDBC URLs that target the same host/db as Control.
        if (looksLikeJdbc(candidate) && samePostgresTarget(candidate, controlNormalized)) {
            throw IsolationViolation(
                "managed database must not use Control's own database connection",
            )
        }
        if (looksLikeJdbc(candidate) &&
            controlUserNormalized.isNotBlank() &&
            candidate.contains("user=$controlUserNormalized")
        ) {
            throw IsolationViolation(
                "managed database must not use Control's own database credentials",
            )
        }
    }

    fun isControlDatabase(endpointOrUrl: String?): Boolean =
        try {
            assertNotControlDatabase(endpointOrUrl)
            false
        } catch (_: IsolationViolation) {
            true
        }

    private fun looksLikeJdbc(value: String): Boolean =
        value.startsWith("postgresql://") || value.startsWith("postgres://") ||
            value.startsWith("jdbc:postgresql://")

    private fun samePostgresTarget(a: String, b: String): Boolean {
        val hostDbA = hostAndDatabase(a) ?: return false
        val hostDbB = hostAndDatabase(b) ?: return false
        return hostDbA == hostDbB
    }

    private fun hostAndDatabase(jdbcish: String): Pair<String, String>? {
        // postgresql://host:port/db or postgresql://host/db
        val withoutScheme = jdbcish
            .removePrefix("jdbc:")
            .removePrefix("postgresql://")
            .removePrefix("postgres://")
        val slash = withoutScheme.indexOf('/')
        if (slash < 0) return null
        val hostPort = withoutScheme.substring(0, slash).substringBefore('?').lowercase()
        val db = withoutScheme.substring(slash + 1).substringBefore('?').substringBefore('/').lowercase()
        if (hostPort.isBlank() || db.isBlank()) return null
        return hostPort to db
    }

    private fun normalize(value: String): String =
        value.trim().lowercase().removePrefix("jdbc:")
}

class IsolationViolation(message: String) : IllegalArgumentException(message)
