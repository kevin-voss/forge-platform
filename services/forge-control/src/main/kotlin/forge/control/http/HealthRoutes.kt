package forge.control.http

import io.ktor.http.HttpStatusCode
import io.ktor.server.response.respond
import io.ktor.server.routing.Route
import io.ktor.server.routing.get
import kotlinx.serialization.Serializable
import java.util.concurrent.atomic.AtomicBoolean

@Serializable
data class HealthResponse(val status: String)

/** Process readiness flag: false until the HTTP server has finished starting. */
class Readiness(initial: Boolean = false) {
    private val ready = AtomicBoolean(initial)

    fun markReady() {
        ready.set(true)
    }

    fun isReady(): Boolean = ready.get()
}

/** Database readiness probe. Returns null when healthy, else an error message. */
fun interface DbProbe {
    fun check(): String?
}

object AlwaysHealthyDb : DbProbe {
    override fun check(): String? = null
}

fun Route.healthRoutes(
    readiness: Readiness,
    dbProbe: DbProbe = AlwaysHealthyDb,
    onDbFailure: (String) -> Unit = {},
) {
    get("/health/live") {
        call.respond(HealthResponse(status = "live"))
    }
    get("/health/ready") {
        if (!readiness.isReady()) {
            call.respond(HttpStatusCode.ServiceUnavailable, HealthResponse(status = "not_ready"))
            return@get
        }
        val dbError = dbProbe.check()
        if (dbError != null) {
            onDbFailure(dbError)
            call.respond(HttpStatusCode.ServiceUnavailable, HealthResponse(status = "not_ready"))
            return@get
        }
        call.respond(HealthResponse(status = "ready"))
    }
}
