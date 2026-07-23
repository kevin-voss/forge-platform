package admin

import io.ktor.server.engine.embeddedServer
import io.ktor.server.netty.Netty
import kotlin.system.exitProcess

fun main() {
    val cfg = try {
        loadConfig()
    } catch (e: IllegalArgumentException) {
        System.err.println("fatal: ${e.message}")
        exitProcess(1)
    }

    val log = JsonLog(cfg.serviceName, cfg.logLevel)
    val server = embeddedServer(Netty, port = cfg.port, host = "0.0.0.0") {
        configureContractRoutes(cfg)
    }

    Runtime.getRuntime().addShutdownHook(
        Thread {
            log.info("shutdown signal received", "signal" to "SIGTERM")
            server.stop(gracePeriodMillis = 1_000, timeoutMillis = 8_000)
            log.info("shutdown complete")
        },
    )

    log.info(
        "listening",
        "port" to cfg.port,
        "version" to cfg.serviceVersion,
        "env" to cfg.env,
    )
    server.start(wait = true)
}
