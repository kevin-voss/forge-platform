package forge.control.db

import com.zaxxer.hikari.HikariConfig
import com.zaxxer.hikari.HikariDataSource
import forge.control.config.DatabaseConfig
import forge.control.logging.JsonLog
import org.flywaydb.core.Flyway
import org.flywaydb.core.api.output.MigrateResult
import java.io.File
import java.sql.SQLException

/** Hikari pool + Flyway migrations + readiness probe (`SELECT 1`). */
class Db(
    val dataSource: HikariDataSource,
    private val schema: String,
) : AutoCloseable {
    fun migrate(): MigrateResult {
        // Fat jars built with zipTree often fail Flyway's classpath directory scan, so
        // prefer an explicit filesystem location when the image ships migrations there.
        val locations = migrationLocations()
        val flyway = Flyway.configure()
            .dataSource(dataSource)
            .schemas(schema)
            .defaultSchema(schema)
            .createSchemas(true)
            .locations(*locations)
            .load()
        return flyway.migrate()
    }

    /** Returns null when healthy; otherwise an error message suitable for logging. */
    fun check(): String? =
        try {
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

    override fun close() {
        dataSource.close()
    }

    companion object {
        fun open(cfg: DatabaseConfig, log: JsonLog? = null): Db {
            val hikari = HikariConfig().apply {
                jdbcUrl = cfg.url
                username = cfg.user
                password = cfg.password
                maximumPoolSize = cfg.poolMax
                poolName = "forge-control"
                connectionInitSql = "SET search_path TO ${cfg.schema}, public"
                isAutoCommit = true
            }
            val ds = HikariDataSource(hikari)
            log?.info(
                "db pool initialized",
                "pool_max" to cfg.poolMax,
                "schema" to cfg.schema,
                "jdbc_host" to jdbcHost(cfg.url),
            )
            return Db(ds, cfg.schema)
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
                return arrayOf("filesystem:${filesystemDir.absolutePath}")
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
