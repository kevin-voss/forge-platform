package forge.identity.db

import com.zaxxer.hikari.HikariConfig
import com.zaxxer.hikari.HikariDataSource
import forge.identity.config.DatabaseConfig
import forge.identity.logging.JsonLog
import org.flywaydb.core.Flyway
import org.flywaydb.core.api.output.MigrateResult
import java.io.File
import java.sql.SQLException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

/** Hikari pool + Flyway migrations + readiness probe (`SELECT 1`). */
class Database(
    val dataSource: HikariDataSource,
) : AutoCloseable {
    private val migrationsApplied = AtomicBoolean(false)
    private val lastError = AtomicReference<String?>(null)

    fun migrate(): MigrateResult {
        val locations = migrationLocations()
        val flyway = Flyway.configure()
            .dataSource(dataSource)
            .locations(*locations)
            .load()
        val result = flyway.migrate()
        migrationsApplied.set(true)
        lastError.set(null)
        return result
    }

    fun markMigrationsApplied() {
        migrationsApplied.set(true)
        lastError.set(null)
    }

    fun markFailure(message: String) {
        migrationsApplied.set(false)
        lastError.set(message)
    }

    fun migrationsReady(): Boolean = migrationsApplied.get()

    fun lastFailure(): String? = lastError.get()

    /** Returns null when healthy; otherwise an error message suitable for logging. */
    fun check(): String? {
        if (!migrationsApplied.get()) {
            return lastError.get() ?: "migrations not applied"
        }
        return try {
            dataSource.connection.use { conn ->
                conn.createStatement().use { stmt ->
                    stmt.executeQuery("SELECT 1").use { rs ->
                        if (rs.next()) null else "SELECT 1 returned no rows"
                    }
                }
            }
        } catch (e: SQLException) {
            e.message ?: e.javaClass.simpleName
        }
    }

    override fun close() {
        dataSource.close()
    }

    companion object {
        fun open(cfg: DatabaseConfig, log: JsonLog? = null): Database {
            val hikari = HikariConfig().apply {
                jdbcUrl = cfg.url
                username = cfg.user
                password = cfg.password
                maximumPoolSize = cfg.poolMax
                poolName = "forge-identity"
                // Fail fast on connect so startup retry loop can back off.
                connectionTimeout = 5_000
                initializationFailTimeout = 1
                isAutoCommit = true
            }
            val ds = HikariDataSource(hikari)
            log?.info(
                "db pool initialized",
                "pool_max" to cfg.poolMax,
                "jdbc_host" to jdbcHost(cfg.url),
            )
            return Database(ds)
        }

        internal fun migrationLocations(
            env: Map<String, String> = System.getenv(),
            filesystemDir: File = File("/app/db/migration"),
        ): Array<String> {
            val override = env["FLYWAY_LOCATIONS"]?.trim().orEmpty()
            if (override.isNotEmpty()) {
                return override.split(',').map { it.trim() }.filter { it.isNotEmpty() }.toTypedArray()
            }
            if (filesystemDir.isDirectory && !filesystemDir.list().isNullOrEmpty()) {
                return arrayOf(
                    "filesystem:${filesystemDir.absolutePath}",
                    "classpath:db/migration",
                )
            }
            return arrayOf("classpath:db/migration")
        }

        private fun jdbcHost(url: String): String {
            // jdbc:postgresql://host:port/db — never log user/password
            val withoutPrefix = url.removePrefix("jdbc:")
            val afterSlash = withoutPrefix.substringAfter("://", missingDelimiterValue = "")
            return afterSlash.substringBefore('/').ifEmpty { "unknown" }
        }
    }
}
