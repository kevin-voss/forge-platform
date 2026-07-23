package forge.control.manageddb

import java.util.UUID

/** Result of a provisioner operation (endpoint refs never equal Control's JDBC URL). */
data class ProvisionResult(
    val endpointRef: String,
    val detail: String? = null,
)

/**
 * Seam over real Postgres ops. Control persists management-plane records;
 * the provisioner creates/attaches/backs up product Postgres elsewhere.
 *
 * Fake now; local Go provisioner lands in later 18.x steps.
 */
interface Provisioner {
    fun createInstance(instanceId: UUID, projectId: UUID, name: String): ProvisionResult
    fun deleteInstance(instanceId: UUID)
    fun createDatabase(instanceId: UUID, databaseName: String): ProvisionResult
    fun createRole(databaseId: UUID, username: String): ProvisionResult
    fun backup(databaseId: UUID): ProvisionResult
    fun restore(databaseId: UUID, backupId: UUID): ProvisionResult
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
        return ProvisionResult(endpointRef = endpoint, detail = "fake-create-instance")
    }

    override fun deleteInstance(instanceId: UUID) {
        // no-op
    }

    override fun createDatabase(instanceId: UUID, databaseName: String): ProvisionResult {
        val endpoint = "fake://managed-db/$instanceId/db/$databaseName"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-create-database")
    }

    override fun createRole(databaseId: UUID, username: String): ProvisionResult {
        val endpoint = "fake://managed-db/role/$databaseId/$username"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-create-role")
    }

    override fun backup(databaseId: UUID): ProvisionResult {
        val endpoint = "fake://managed-db/backup/$databaseId"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-backup")
    }

    override fun restore(databaseId: UUID, backupId: UUID): ProvisionResult {
        val endpoint = "fake://managed-db/restore/$databaseId/$backupId"
        isolation.assertNotControlDatabase(endpoint)
        return ProvisionResult(endpointRef = endpoint, detail = "fake-restore")
    }

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
        value.startsWith("postgresql://") || value.startsWith("postgres://")

    private fun samePostgresTarget(a: String, b: String): Boolean {
        val hostDbA = hostAndDatabase(a) ?: return false
        val hostDbB = hostAndDatabase(b) ?: return false
        return hostDbA == hostDbB
    }

    private fun hostAndDatabase(jdbcish: String): Pair<String, String>? {
        // postgresql://host:port/db or postgresql://host/db
        val withoutScheme = jdbcish.removePrefix("postgresql://").removePrefix("postgres://")
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
