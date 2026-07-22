package forge.control

import forge.control.config.loadAppConfig
import forge.control.db.Db
import forge.control.logging.JsonLog
import kotlin.system.exitProcess

/** Runs Flyway migrations without starting the HTTP server (`make migrate`). */
fun main() {
    val cfg = try {
        loadAppConfig()
    } catch (e: IllegalArgumentException) {
        System.err.println("fatal: ${e.message}")
        exitProcess(1)
    }

    val log = JsonLog(cfg.serviceName, cfg.logLevel)
    val db = try {
        Db.open(cfg.database, log)
    } catch (e: Exception) {
        log.error("db pool failed to initialize", "error" to (e.message ?: e.javaClass.simpleName))
        exitProcess(1)
    }

    try {
        val result = db.migrate()
        log.info(
            "migrations applied",
            "from" to result.initialSchemaVersion,
            "to" to result.targetSchemaVersion,
            "migrations_executed" to result.migrationsExecuted,
        )
    } catch (e: Exception) {
        log.error("migration failed", "error" to (e.message ?: e.javaClass.simpleName))
        exitProcess(1)
    } finally {
        db.close()
    }
}
